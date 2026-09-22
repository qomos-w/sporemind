package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/actor/shell"
	"github.com/qomos-w/sporemind/pkg/converter"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"

	"github.com/qomos-w/sporemind/pkg/agentkit"
	"github.com/qomos-w/sporemind/pkg/flagparse"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// cancelledToolMsg 是 callTool / callStreamingTool 在 context 被取消时返回的消息。
// 用于检测工具是否因 pause/cancel 而被中断。
const cancelledToolMsg = "tool call cancelled: turn is shutting down"

// pausedToolMsg 是工具因用户暂停而中断时写入的 tool_result 文本。
const pausedToolMsg = "Tool execution paused by user."

// phaseExecute 将当前工具 batch 执行到完成。
// 它 inline 处理 ask_user、权限确认、fork_child 等待，
// 保证函数返回时该 batch 已完全解决。
//
// 流程图：
//
//	┌─────────────────────────┐
//	│  batch 含 ask_user ?     │ 是 → executeWaitUser ──→ advanceBatch
//	└───────────┬─────────────┘
//	            ↓ 否
//	┌─────────────────────────┐
//	│   checkBatchPermission   │
//	└───────────┬─────────────┘
//	            ↓
//	    ┌───────┼───────┐
//	    ↓ deny  ↓ allow ↓ confirm
//	 buildDenied    executeToolBatch  executeWaitPermission
//	 processBatchResults  trackForkChildren   advanceBatch
//	 advanceBatch    processBatchResults
//	                 executeWaitChildren(如有)
//	                 advanceBatch
func (e *turnEngine) phaseExecute(ctx actor.Context, turnID string) error {
	batch := e.batches[e.batchIdx]

	// 如果 batch 里有 ask_user，优先进入交互式等待。
	if askCall := findAskUser(batch); askCall != nil {
		// 自主模式（yolo/autopilot）屏蔽 ask_user：agent 必须自主决策，不得向用户提问。
		if e.isAutonomousMode() {
			if err := e.executeBlockAskUser(ctx, turnID, askCall, batch); err != nil {
				return err
			}
			e.advanceBatch(ctx, turnID)
			return nil
		}
		if err := e.executeWaitUser(ctx, turnID, askCall, batch); err != nil {
			return err
		}
		e.advanceBatch(ctx, turnID)
		return nil
	}

	goalIdx := findGoalSubmit(batch)
	cardIdx := findGoalCardSubmit(batch)
	workflowIdx := findWorkflowStart(batch)
	workflowPlanIdx := findWorkflowPlanSubmit(batch)
	if goalIdx >= 0 && cardIdx >= 0 {
		return fmt.Errorf("turnEngine: goal_submit and goal_card_submit cannot be called in the same batch")
	}
	if workflowIdx >= 0 && (goalIdx >= 0 || cardIdx >= 0) {
		return fmt.Errorf("turnEngine: workflow_start cannot be combined with goal_submit or goal_card_submit in the same batch")
	}
	if workflowPlanIdx >= 0 && (goalIdx >= 0 || cardIdx >= 0 || workflowIdx >= 0) {
		return fmt.Errorf("turnEngine: workflow_plan_submit cannot be combined with goal_submit, goal_card_submit, or workflow_start in the same batch")
	}

	// 如果 batch 里有 goal.submit，优先进入目标确认等待。
	if goalIdx >= 0 {
		goalCall := &batch.calls[goalIdx]
		otherCalls := make([]pendingToolCall, 0, len(batch.calls)-1)
		otherSteps := make([]domain.TurnAction, 0, len(batch.calls)-1)
		for i, c := range batch.calls {
			if i == goalIdx {
				continue
			}
			otherCalls = append(otherCalls, c)
			otherSteps = append(otherSteps, e.batchSteps[i])
		}
		if len(otherCalls) > 0 {
			otherBatch := toolExecutionBatch{calls: otherCalls}
			results := e.executeToolBatch(ctx, otherBatch)
			if err := e.processBatchResults(ctx, turnID, results, otherSteps); err != nil {
				return err
			}
			if err := e.flushEvents(ctx); err != nil {
				e.logger.Error("turnEngine: post-goal-readonly flushEvents failed", "err", err)
			}
			if err := e.flushTurnEvents(ctx); err != nil {
				e.logger.Error("turnEngine: post-goal-readonly flushTurnEvents failed", "err", err)
			}
		}
		if err := e.executeWaitGoalSubmit(ctx, turnID, goalCall); err != nil {
			return err
		}
		e.advanceBatch(ctx, turnID)
		return nil
	}

	// 如果 batch 里有 goal_card_submit，先执行同批其他只读调用，再进入任务卡确认等待。
	if cardIdx >= 0 {
		cardCall := &batch.calls[cardIdx]
		otherCalls := make([]pendingToolCall, 0, len(batch.calls)-1)
		otherSteps := make([]domain.TurnAction, 0, len(batch.calls)-1)
		for i, c := range batch.calls {
			if i == cardIdx {
				continue
			}
			otherCalls = append(otherCalls, c)
			otherSteps = append(otherSteps, e.batchSteps[i])
		}
		if len(otherCalls) > 0 {
			otherBatch := toolExecutionBatch{calls: otherCalls}
			results := e.executeToolBatch(ctx, otherBatch)
			if err := e.processBatchResults(ctx, turnID, results, otherSteps); err != nil {
				return err
			}
			if err := e.flushEvents(ctx); err != nil {
				e.logger.Error("turnEngine: post-goal-card-readonly flushEvents failed", "err", err)
			}
			if err := e.flushTurnEvents(ctx); err != nil {
				e.logger.Error("turnEngine: post-goal-card-readonly flushTurnEvents failed", "err", err)
			}
		}
		if err := e.executeWaitGoalCardSubmit(ctx, turnID, cardCall); err != nil {
			return err
		}
		e.advanceBatch(ctx, turnID)
		return nil
	}

	// 如果 batch 里有 workflow_start，先执行同批其他只读调用，再进入 workflow
	// map 启动确认等待。确认批准后才会真正激活 workflow 并持久化状态。
	if workflowIdx >= 0 {
		workflowCall := &batch.calls[workflowIdx]
		otherCalls := make([]pendingToolCall, 0, len(batch.calls)-1)
		otherSteps := make([]domain.TurnAction, 0, len(batch.calls)-1)
		for i, c := range batch.calls {
			if i == workflowIdx {
				continue
			}
			otherCalls = append(otherCalls, c)
			otherSteps = append(otherSteps, e.batchSteps[i])
		}
		if len(otherCalls) > 0 {
			otherBatch := toolExecutionBatch{calls: otherCalls}
			results := e.executeToolBatch(ctx, otherBatch)
			if err := e.processBatchResults(ctx, turnID, results, otherSteps); err != nil {
				return err
			}
			if err := e.flushEvents(ctx); err != nil {
				e.logger.Error("turnEngine: post-workflow-start-readonly flushEvents failed", "err", err)
			}
			if err := e.flushTurnEvents(ctx); err != nil {
				e.logger.Error("turnEngine: post-workflow-start-readonly flushTurnEvents failed", "err", err)
			}
		}
		if err := e.executeWaitWorkflowStart(ctx, turnID, workflowCall); err != nil {
			return err
		}
		e.advanceBatch(ctx, turnID)
		return nil
	}

	// 如果 batch 里有 workflow_plan_submit，先执行同批其他只读调用，再进入
	// workflow 确认等待。确认批准后才会真正激活 workflow。
	if workflowPlanIdx >= 0 {
		planCall := &batch.calls[workflowPlanIdx]
		otherCalls := make([]pendingToolCall, 0, len(batch.calls)-1)
		otherSteps := make([]domain.TurnAction, 0, len(batch.calls)-1)
		for i, c := range batch.calls {
			if i == workflowPlanIdx {
				continue
			}
			otherCalls = append(otherCalls, c)
			otherSteps = append(otherSteps, e.batchSteps[i])
		}
		if len(otherCalls) > 0 {
			otherBatch := toolExecutionBatch{calls: otherCalls}
			results := e.executeToolBatch(ctx, otherBatch)
			if err := e.processBatchResults(ctx, turnID, results, otherSteps); err != nil {
				return err
			}
			if err := e.flushEvents(ctx); err != nil {
				e.logger.Error("turnEngine: post-workflow-plan-readonly flushEvents failed", "err", err)
			}
			if err := e.flushTurnEvents(ctx); err != nil {
				e.logger.Error("turnEngine: post-workflow-plan-readonly flushTurnEvents failed", "err", err)
			}
		}
		if err := e.executeWaitWorkflowPlanSubmit(ctx, turnID, planCall); err != nil {
			return err
		}
		e.advanceBatch(ctx, turnID)
		return nil
	}

	// 如果 batch 里有 plan.submit，先执行同批其他只读调用，再进入计划审批等待。
	if planIdx := findPlanSubmit(batch); planIdx >= 0 {
		planCall := &batch.calls[planIdx]
		otherCalls := make([]pendingToolCall, 0, len(batch.calls)-1)
		otherSteps := make([]domain.TurnAction, 0, len(batch.calls)-1)
		for i, c := range batch.calls {
			if i == planIdx {
				continue
			}
			otherCalls = append(otherCalls, c)
			otherSteps = append(otherSteps, e.batchSteps[i])
		}
		if len(otherCalls) > 0 {
			otherBatch := toolExecutionBatch{calls: otherCalls}
			results := e.executeToolBatch(ctx, otherBatch)
			if err := e.processBatchResults(ctx, turnID, results, otherSteps); err != nil {
				return err
			}
			if err := e.flushEvents(ctx); err != nil {
				e.logger.Error("turnEngine: post-plan-readonly flushEvents failed", "err", err)
			}
			if err := e.flushTurnEvents(ctx); err != nil {
				e.logger.Error("turnEngine: post-plan-readonly flushTurnEvents failed", "err", err)
			}
		}
		if err := e.executeWaitPlanApproval(ctx, turnID, planCall); err != nil {
			return err
		}
		e.advanceBatch(ctx, turnID)
		return nil
	}

	// 如果 batch 里有 agent_wait，先执行同批其他调用（通常含 async fork
	// 的 spawn），再进入收割等待。agent_wait 是 async fork 的收割端点，
	// 必须排在同批 spawn 之后。
	if waitIdx := findAgentWait(batch); waitIdx >= 0 {
		waitCall := &batch.calls[waitIdx]
		otherCalls := make([]pendingToolCall, 0, len(batch.calls)-1)
		otherSteps := make([]domain.TurnAction, 0, len(batch.calls)-1)
		for i, c := range batch.calls {
			if i == waitIdx {
				continue
			}
			otherCalls = append(otherCalls, c)
			otherSteps = append(otherSteps, e.batchSteps[i])
		}
		if len(otherCalls) > 0 {
			otherBatch := toolExecutionBatch{calls: otherCalls}
			results := e.executeToolBatch(ctx, otherBatch)
			e.trackForkChildren(ctx, otherBatch, results)
			if err := e.processBatchResults(ctx, turnID, results, otherSteps); err != nil {
				return err
			}
			if err := e.flushEvents(ctx); err != nil {
				e.logger.Error("turnEngine: post-agent-wait-readonly flushEvents failed", "err", err)
			}
			if err := e.flushTurnEvents(ctx); err != nil {
				e.logger.Error("turnEngine: post-agent-wait-readonly flushTurnEvents failed", "err", err)
			}
		}
		// 同批其他 fork 若是同步的，也先等它们完成（agent_wait 只收割 async）。
		if e.hasSyncPendingChildren() {
			if err := e.executeWaitChildren(ctx, turnID); err != nil {
				return err
			}
		}
		if err := e.executeWaitAgents(ctx, turnID, waitCall); err != nil {
			return err
		}
		e.advanceBatch(ctx, turnID)
		return nil
	}

	// 否则先进行权限决策。
	decision, reason := e.checkBatchPermission(ctx, batch)
	switch decision {
	case "deny":
		// 用户拒绝：给 batch 中每个调用生成权限拒绝错误结果。
		if err := e.processBatchResults(ctx, turnID, buildDeniedResults(batch, reason), e.batchSteps); err != nil {
			return err
		}
		e.advanceBatch(ctx, turnID)
		return nil

	case "confirm":
		// 需要用户确认：进入 permission 等待流程。
		e.permissionReason = reason
		if err := e.executeWaitPermission(ctx, turnID, batch); err != nil {
			return err
		}
		e.advanceBatch(ctx, turnID)
		return nil

	case "bypass":
		// bypass 模式：由 fast model 单次非上下文调用决定是否放行。
		allowed, bypassReason := e.executeBypassApproval(ctx, batch)
		e.reportBypassDecision(ctx, turnID, batch, allowed, bypassReason)
		if !allowed {
			if err := e.processBatchResults(ctx, turnID, buildDeniedResults(batch, bypassReason), e.batchSteps); err != nil {
				return err
			}
			e.advanceBatch(ctx, turnID)
			return nil
		}
		// 放行：落入下方正常执行路径。
	}

	// 正常执行路径：调用工具。
	results := e.executeToolBatch(ctx, batch)
	// 记录成功的 fork_child 为待等待子 agent。
	e.trackForkChildren(ctx, batch, results)

	if err := e.processBatchResults(ctx, turnID, results, e.batchSteps); err != nil {
		return err
	}

	// 立即 flush 非 fork_child step 的 block.appended / step.closed 事件，
	// 不要让前端一直等到 executeWaitChildren 结束后才看到普通 step 完成。
	if err := e.flushEvents(ctx); err != nil {
		e.logger.Error("turnEngine: post-batch flushEvents failed", "err", err)
	}
	if err := e.flushTurnEvents(ctx); err != nil {
		e.logger.Error("turnEngine: post-batch flushTurnEvents failed", "err", err)
	}

	// 如果本轮产生了同步 fork_child，等待它们完成。Async 子 agent 不阻塞，
	// 由 agent_wait 显式收割。
	if e.hasSyncPendingChildren() {
		if err := e.executeWaitChildren(ctx, turnID); err != nil {
			return err
		}
	}

	e.advanceBatch(ctx, turnID)
	return nil
}

// executeWaitUser 发出 ask_user 事件，阻塞在 resumeCh 上等待用户回答，
// 然后将答案作为 tool_result 追加到历史。
//
// 流程图：
//
//	┌─────────────────────────┐
//	│   生成 requestID         │
//	│   解析 questions         │
//	│   emit ask_user 事件      │
//	└───────────┬─────────────┘
//	            ↓
//	┌─────────────────────────┐
//	│    select { ... }       │
//	│  resumeCh: 读答案        │
//	│  done/ctx.Done: 取消     │
//	└───────────┬─────────────┘
//	            ↓ 收到答案
//	┌─────────────────────────┐
//	│  appendMessage(tool)    │
//	│  关闭 batchSteps         │
//	└─────────────────────────┘
func (e *turnEngine) executeWaitUser(ctx actor.Context, turnID string, askCall *pendingToolCall, batch toolExecutionBatch) error {
	requestID := askCall.ID
	e.resumeRequestID = requestID

	// 先规范化 ask_user 输入：LLM 偶尔会把 questions 数组双重编码为 JSON 字符串。
	// 规范化失败（格式不可恢复）时，向 agent 回写错误 tool_result，让其重新提问。
	normalizedInput, normErr := normalizeAskUserInput(askCall.Input)
	if normErr != nil {
		errResult := toolExecutionResult{
			call:  *askCall,
			out:   fmt.Sprintf("ask_user format error: %v. The 'questions' field must be an array of objects, each with 'header', 'question', 'options' (array of {label, description}), and 'multiSelect'.", normErr),
			isErr: true,
		}
		return e.processBatchResults(ctx, turnID, []toolExecutionResult{errResult}, e.batchSteps)
	}
	askCall.Input = normalizedInput

	// 过滤掉 header 和 question 都过短的问题（AND）。
	// LLM 偶尔会产出 "1"/"ok" 这种无意义的简短问题；过滤后让用户只看到符合
	// 最低质量标准的问题。如果全部被剔除，写错误 tool_result 让 LLM 重新提问。
	filteredInput, allFiltered, filterErr := filterShortAskUserQuestions(askCall.Input)
	if filterErr != nil {
		e.logger.Warn("turnEngine: filter ask_user questions failed, using raw input",
			"error", filterErr, "input", truncate(askCall.Input, 200))
	} else {
		askCall.Input = filteredInput
	}
	if allFiltered {
		errResult := toolExecutionResult{
			call:  *askCall,
			out:   "all ask_user questions were filtered: don't ask meaningless questions",
			isErr: true,
		}
		return e.processBatchResults(ctx, turnID, []toolExecutionResult{errResult}, e.batchSteps)
	}

	// Emit step.interaction_requested for ask_user so the frontend shows questions.
	// 用 json.Unmarshal 把 askCall.Input 解析为 map，避免将 JSON 字符串再次转义。
	stepID := turnID + "-ask-" + requestID
	var payload map[string]any
	if len(askCall.Input) > 0 {
		payload = map[string]any{}
		if err := json.Unmarshal([]byte(askCall.Input), &payload); err != nil {
			payload = map[string]any{"questions": []any{}}
		}
	} else {
		payload = map[string]any{"questions": []any{}}
	}
	_ = e.emitStepImmediate(ctx, domain.StepEvent{
		Kind:            "step.interaction_requested",
		StepID:          stepID,
		TurnID:          turnID,
		InteractionType: "ask_user",
		RequestID:       requestID,
		Task:            payload,
	})
	if e.onAskUser != nil {
		e.onAskUser()
	}

	select {
	case <-e.resumeCh:
		answer := e.resumeAnswer
		e.resumeAnswer = ""
		e.resumeRequestID = ""
		return e.applyAskUserAnswer(ctx, turnID, askCall, answer)
		// NOTE: no pauseDone case here. A pending ask_user is itself the
		// user-input gate; abandoning it on pause would discard the question
		// and lose the answer on resume. handleTurnPause no-ops while an
		// interaction is pending, and even if a pause races through, this
		// select ignores it and keeps waiting for the answer.
	case <-e.done:
		e.writeCancelledToolResults(ctx, turnID, []pendingToolCall{*askCall})
		return context.Canceled
	case <-ctx.Lifecycle().Done():
		e.writeCancelledToolResults(ctx, turnID, []pendingToolCall{*askCall})
		return context.Canceled
	}
}

// executeWaitPermission 发出权限请求事件，阻塞在 resumeCh 上，
// 根据用户回答决定执行 batch 还是标记为拒绝。
//
// 流程图：
//
//	┌─────────────────────────┐
//	│  生成 requestID          │
//	│  构建 ToolCallSummary    │
//	│  emit permission_requested│
//	└───────────┬─────────────┘
//	            ↓
//	┌─────────────────────────┐
//	│    select { ... }       │
//	└───────────┬─────────────┘
//	            ↓ 收到答案
//	┌─────────────────────────┐
//	│  answer.Allowed == true? │──是→ executeToolBatch → processBatchResults
//	└───────────┬─────────────┘
//	            ↓ 否
//	┌─────────────────────────┐
//	│  buildDeniedResults      │
//	│  processBatchResults     │
//	└─────────────────────────┘
func (e *turnEngine) executeWaitPermission(ctx actor.Context, turnID string, batch toolExecutionBatch) error {
	requestID := ""
	if len(batch.calls) > 0 {
		requestID = batch.calls[0].ID
	} else {
		requestID = ctx.NewID().String()
	}
	e.resumeRequestID = requestID

	summaries := buildToolCallSummaries(batch)
	// Registration callables get their requested host capabilities resolved
	// and attached so the permission panel shows them before approval.
	e.enrichRegistrationSummaries(ctx, summaries)
	e.emitPermissionEvent(ctx, turnID, requestID, summaries)

	select {
	case <-e.resumeCh:
		answer := e.resumeAnswer
		e.resumeAnswer = ""
		// Capture requestID before clearing so the resolution event targets
		// the same step ID that was broadcast to all clients.
		resolvedRequestID := e.resumeRequestID
		e.resumeRequestID = ""
		return e.applyPermissionAnswer(ctx, turnID, resolvedRequestID, batch, answer)
		// NOTE: no pauseDone case — see executeWaitAskUser. A pending
		// permission request is the user-input gate and must not be abandoned
		// by a pause; keep waiting for the user's allow/deny answer.
	case <-e.done:
		e.writeCancelledToolResults(ctx, turnID, batch.calls)
		return context.Canceled
	case <-ctx.Lifecycle().Done():
		e.writeCancelledToolResults(ctx, turnID, batch.calls)
		return context.Canceled
	}
}

// executeWaitChildren 等待所有 fork 子 agent 的权威终态回传。
// 父端 idle timeout 仅作为失联兜底：child 通过独立 ticker 每
// ChildHeartbeatInterval 发送 explore_progress 刷新计时；只有当 child
// 真正停止活动（崩溃/panic/网络全断）超过 ChildAgentIdleTimeout 时才超时。
//
// 不变式：ChildAgentIdleTimeout 必须大于 child 单次 dispatch iteration
// 的最长静默期（StreamIdleTimeout 或 PlanGoalStreamIdleTimeout）。child
// ticker 保证在 LLM 思考/工具执行期间持续刷新，故正常运行永不误判。
//
// 流程图：
//
//	┌─────────────────────────┐
//	│ pendingChildren > 0 ?   │
//	└───────────┬─────────────┘
//	            ↓ 是
//	┌─────────────────────────┐
//	│  计算最早超时 deadline   │
//	└───────────┬─────────────┘
//	            ↓
//	┌─────────────────────────┐
//	│    select { ... }       │
//	│ childDoneCh: handleChildResult   │
//	│ timerCh:     handleChildTimeout  │
//	│ progressCh:  重算 deadline       │
//	│ cancel:      返回 Canceled       │
//	└───────────┬─────────────┘
//	            ↓
//	┌─────────────────────────┐
//	│    flush 事件           │
//	└─────────────────────────┘
func (e *turnEngine) executeWaitChildren(ctx actor.Context, turnID string) error {
	pollTicker := time.NewTicker(domain.ChildHeartbeatInterval)
	defer pollTicker.Stop()
	for len(e.pendingChildren) > 0 {
		e.refreshChildLiveness(ctx)
		deadline := e.earliestChildDeadline()
		var timer *time.Timer
		var timerCh <-chan time.Time
		if !deadline.IsZero() {
			timer = time.NewTimer(time.Until(deadline))
			timerCh = timer.C
		}

		select {
		case result := <-e.childDoneCh:
			if timer != nil {
				timer.Stop()
			}
			e.handleChildResult(ctx, turnID, result)
			// 每个子 agent 完成后立即 flush，让前端实时看到进度，
			// 而不是等所有子 agent 都结束才一次性刷新。
			if err := e.flushEvents(ctx); err != nil {
				e.logger.Error("turnEngine: child-result flush failed", "err", err)
			}
			if err := e.flushTurnEvents(ctx); err != nil {
				e.logger.Error("turnEngine: child-result turn-flush failed", "err", err)
			}

		case <-timerCh:
			e.handleChildTimeout(ctx, turnID)
			if err := e.flushEvents(ctx); err != nil {
				e.logger.Error("turnEngine: child-timeout flush failed", "err", err)
			}
			if err := e.flushTurnEvents(ctx); err != nil {
				e.logger.Error("turnEngine: child-timeout turn-flush failed", "err", err)
			}

		case <-e.childProgressCh:
			if timer != nil {
				timer.Stop()
			}
		case <-pollTicker.C:
			e.refreshChildLiveness(ctx)
			if timer != nil {
				timer.Stop()
			}

		case <-e.pauseDone():
			if timer != nil {
				timer.Stop()
			}
			return nil
		case <-e.done:
			if timer != nil {
				timer.Stop()
			}
			return context.Canceled
		case <-ctx.Lifecycle().Done():
			if timer != nil {
				timer.Stop()
			}
			return context.Canceled
		}
	}
	return nil
}

// agent_wait 超时钳制：下限 10s，缺省 30s，硬顶 1h（超出直接报错，
// 与 codex wait_agent 的三级钳制语义一致）。var 而非 const：单元测试
// 需要临时压低下限验证超时路径，生产路径不得改动。
var (
	agentWaitMinTimeout     = 10 * time.Second
	agentWaitDefaultTimeout = 30 * time.Second
	agentWaitMaxTimeout     = time.Hour
)

type agentWaitInput struct {
	AgentIds  []string `json:"AgentIds"`
	TimeoutMs float64  `json:"TimeoutMs"`
}

// findAgentWait 返回 batch 中的 agent_wait 调用下标，无则 -1。
func findAgentWait(batch toolExecutionBatch) int {
	for i, call := range batch.calls {
		if call.CallableID == "agent_wait" {
			return i
		}
	}
	return -1
}

// agentWaitOutcomeRow 是 agent_wait 工具结果中的一行子 agent 状态。
type agentWaitOutcomeRow struct {
	AgentID      string `json:"AgentId"`
	ToolUseID    string `json:"ToolUseId,omitempty"`
	Status       string `json:"Status"`
	Summary      string `json:"Summary,omitempty"`
	Iterations   int32  `json:"Iterations,omitempty"`
	SearchCount  int32  `json:"SearchCount,omitempty"`
	ReadCount    int32  `json:"ReadCount,omitempty"`
	InputTokens  int32  `json:"InputTokens,omitempty"`
	OutputTokens int32  `json:"OutputTokens,omitempty"`
	Error        string `json:"Error,omitempty"`
	Note         string `json:"Note,omitempty"`
}

type agentWaitOutput struct {
	TimedOut bool                  `json:"TimedOut"`
	Results  []agentWaitOutcomeRow `json:"Results"`
}

// executeWaitAgents 是 agent_wait 的收割循环：阻塞等待 async fork 子 agent
// 完成（或超时），把每个目标的终态作为工具结果返回。
//
// 语义：
//   - 目标为空 = 当前所有仍在运行的 async 子 agent
//   - 显式 AgentIds 可命中：运行中（asyncPending）、本 turn 已完成
//     （asyncChildResults）、历史完成（ExploreResults 回查，跨 turn 收割）
//   - 超时不报错：返回部分结果 + TimedOut:true，仍在运行的列为 running
//   - 取消（done / lifecycle）：中断等待返回 Canceled，子 agent 不受影响
//   - 暂停：立即以当前状态收尾工具结果（协议平衡），随 turn 一起暂停
func (e *turnEngine) executeWaitAgents(ctx actor.Context, turnID string, waitCall *pendingToolCall) error {
	e.logger.Info("turnEngine: agent_wait", "turnID", turnID, "toolUseID", waitCall.ID, "input", waitCall.Input)

	var input agentWaitInput
	if err := json.Unmarshal([]byte(waitCall.Input), &input); err != nil {
		e.finishAgentWait(ctx, turnID, waitCall, fmt.Sprintf("agent_wait: invalid arguments JSON: %v", err), true)
		return nil
	}
	timeout := agentWaitDefaultTimeout
	switch {
	case input.TimeoutMs > 0 && input.TimeoutMs < float64(agentWaitMinTimeout.Milliseconds()):
		timeout = agentWaitMinTimeout
	case input.TimeoutMs == 0:
		// default
	case input.TimeoutMs > float64(agentWaitMaxTimeout.Milliseconds()):
		e.finishAgentWait(ctx, turnID, waitCall, fmt.Sprintf("agent_wait: TimeoutMs %d exceeds the %s hard cap; resend with a smaller timeout", int64(input.TimeoutMs), agentWaitMaxTimeout), true)
		return nil
	default:
		timeout = time.Duration(input.TimeoutMs * float64(time.Millisecond))
	}

	// 解析等待目标：仍在运行的进 pendingTargets。已完成的目标无需等待，
	// collectAgentWaitRows 会按 asyncChildResults / ExploreResults 回查。
	var pendingTargets []pendingChild
	for _, pc := range e.asyncPending {
		if len(input.AgentIds) > 0 && !agentWaitTargetsAgent(input.AgentIds, pc.AgentID) {
			continue
		}
		pendingTargets = append(pendingTargets, pc)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	timedOut := false
waitLoop:
	for len(pendingTargets) > 0 {
		select {
		case result := <-e.childDoneCh:
			e.handleChildResult(ctx, turnID, result)
			pendingTargets = dropPendingTarget(pendingTargets, result.ToolUseID)
			if err := e.flushEvents(ctx); err != nil {
				e.logger.Error("turnEngine: agent_wait child-result flush failed", "err", err)
			}
			if err := e.flushTurnEvents(ctx); err != nil {
				e.logger.Error("turnEngine: agent_wait child-result turn-flush failed", "err", err)
			}

		case <-e.childProgressCh:
			// 子 agent 进度心跳：保持等待。
		case <-timer.C:
			timedOut = true
			break waitLoop

		case <-e.pauseDone():
			// 暂停：立即以当前状态收尾，保证 tool_use/tool_result 协议平衡。
			e.finishAgentWaitPaused(ctx, turnID, waitCall)
			return nil
		case <-e.done:
			return context.Canceled
		case <-ctx.Lifecycle().Done():
			return context.Canceled
		}
	}

	output := agentWaitOutput{TimedOut: timedOut, Results: e.collectAgentWaitRows(input.AgentIds)}
	e.finishAgentWait(ctx, turnID, waitCall, renderAgentWaitOutput(output), false)
	return nil
}

func agentWaitTargetsAgent(ids []string, agentID string) bool {
	for _, id := range ids {
		if id == agentID {
			return true
		}
	}
	return false
}

func dropPendingTarget(pending []pendingChild, toolUseID string) []pendingChild {
	for i, pc := range pending {
		if pc.ToolUseID == toolUseID {
			return append(pending[:i], pending[i+1:]...)
		}
	}
	return pending
}

// lookupAsyncOutcomeByAgentID finds a finished-this-turn async child by agent ID.
func (e *turnEngine) lookupAsyncOutcomeByAgentID(agentID string) (asyncChildOutcome, bool) {
	if agentID == "" {
		return asyncChildOutcome{}, false
	}
	for _, outcome := range e.asyncChildResults {
		if outcome.AgentID == agentID {
			return outcome, true
		}
	}
	return asyncChildOutcome{}, false
}

// collectAgentWaitRows assembles the per-child outcome rows, resolving each
// target live at snapshot time: still running (asyncPending), finished this
// turn (asyncChildResults), finished in an earlier turn (ExploreResults
// callback), else unknown. Explicit AgentIds keep the caller's order; an
// empty list snapshots every async child of the turn.
func (e *turnEngine) collectAgentWaitRows(agentIDs []string) []agentWaitOutcomeRow {
	if len(agentIDs) > 0 {
		rows := make([]agentWaitOutcomeRow, 0, len(agentIDs))
		for _, id := range agentIDs {
			if pc, ok := e.findAsyncPendingByAgentID(id); ok {
				rows = append(rows, agentWaitOutcomeRow{AgentID: id, ToolUseID: pc.ToolUseID, Status: "running"})
				continue
			}
			if outcome, ok := e.lookupAsyncOutcomeByAgentID(id); ok {
				rows = append(rows, agentWaitOutcomeRowFrom(id, outcome))
				continue
			}
			if e.onExploreResultLookup != nil {
				if res, ok := e.onExploreResultLookup(id); ok {
					rows = append(rows, agentWaitOutcomeRow{AgentID: id, Status: "completed", Summary: res.Summary, SearchCount: res.SearchCount, ReadCount: res.ReadCount, InputTokens: res.InputTokens, OutputTokens: res.OutputTokens})
					continue
				}
			}
			rows = append(rows, agentWaitOutcomeRow{AgentID: id, Status: "unknown", Note: "no fork child with this id; child agent ids come from Async=true fork responses"})
		}
		return rows
	}
	rows := make([]agentWaitOutcomeRow, 0, len(e.asyncPending)+len(e.asyncChildResults))
	for _, pc := range e.asyncPending {
		rows = append(rows, agentWaitOutcomeRow{AgentID: pc.AgentID, ToolUseID: pc.ToolUseID, Status: "running"})
	}
	for _, outcome := range e.asyncChildResults {
		rows = append(rows, agentWaitOutcomeRowFrom(outcome.AgentID, outcome))
	}
	return rows
}

func (e *turnEngine) findAsyncPendingByAgentID(agentID string) (pendingChild, bool) {
	for _, pc := range e.asyncPending {
		if pc.AgentID == agentID {
			return pc, true
		}
	}
	return pendingChild{}, false
}

func agentWaitOutcomeRowFrom(agentID string, outcome asyncChildOutcome) agentWaitOutcomeRow {
	row := agentWaitOutcomeRow{
		AgentID:      agentID,
		ToolUseID:    "",
		Status:       outcome.Status,
		Summary:      outcome.Result.Summary,
		Iterations:   outcome.Result.Iterations,
		SearchCount:  outcome.Result.SearchCount,
		ReadCount:    outcome.Result.ReadCount,
		InputTokens:  outcome.Result.InputTokens,
		OutputTokens: outcome.Result.OutputTokens,
		Error:        outcome.Err,
	}
	return row
}

func renderAgentWaitOutput(output agentWaitOutput) string {
	b, err := json.Marshal(output)
	if err != nil {
		return `{"TimedOut":false,"Results":[]}`
	}
	return string(b)
}

// finishAgentWait writes the agent_wait tool_result (msg), closes its step,
// and decrements openToolCalls — the single place that finalizes the call.
func (e *turnEngine) finishAgentWait(ctx actor.Context, turnID string, waitCall *pendingToolCall, msg string, isErr bool) {
	if e.openToolCalls > 0 {
		e.openToolCalls--
	}
	e.appendMessage(domain.ChatMessage{
		ID:      ctx.NewID().String(),
		Role:    "tool",
		Content: []domain.ContentBlock{{Type: domain.ContentBlockToolResult, ToolUseID: waitCall.ID, Text: msg, IsError: isErr}},
	})
	for i := range e.batchSteps {
		step := e.batchSteps[i]
		if step.ToolUseID != waitCall.ID {
			continue
		}
		step.Output = msg
		step.Text = msg
		if isErr {
			step.State = "failed"
		} else {
			step.State = "completed"
		}
		e.upsertStep(step)
		e.actionCount++
		e.emitStepEvent(domain.StepEvent{
			Kind:   "block.appended",
			StepID: step.ID,
			TurnID: turnID,
			Block: &domain.ContentBlock{
				Type:      domain.ContentBlockToolResult,
				ToolUseID: waitCall.ID,
				Text:      msg,
				IsError:   isErr,
			},
		})
		if isErr {
			e.emitStepEvent(domain.StepEvent{Kind: "step.error", StepID: step.ID, TurnID: turnID, Error: msg})
		} else {
			e.emitStepEvent(domain.StepEvent{Kind: "step.closed", StepID: step.ID, TurnID: turnID})
		}
		return
	}
	e.actionCount++
}

// finishAgentWaitPaused snapshots the current wait state as the tool result
// when the turn pauses mid-wait, keeping the tool_use/tool_result protocol
// balanced without killing the children.
func (e *turnEngine) finishAgentWaitPaused(ctx actor.Context, turnID string, waitCall *pendingToolCall) {
	output := agentWaitOutput{Results: e.collectAgentWaitRows(nil)}
	msg := renderAgentWaitOutput(output) + "\n(wait interrupted by pause — children are unaffected; call agent_wait again after resume to re-collect)"
	e.finishAgentWait(ctx, turnID, waitCall, msg, false)
}

// writeCancelledToolResults 为取消的 turn 写出 tool_result 消息，
// 防止历史上出现孤儿 tool_use 块（assistant tool_use 已在 dispatch 时写入 history）。
// 同时 emit step 事件，使被取消 tool_use 对应的 tool_call step 拿到 tool_result
// 块并关闭——否则持久化的 cancelled turn 只包含 tool_use，下一轮 dispatch 会
// 把它当成孤儿（即便 sanitizeMessages 会剥离，持久化层也应保持协议平衡）。
func (e *turnEngine) writeCancelledToolResults(ctx actor.Context, turnID string, calls []pendingToolCall) {
	e.writeToolResultsWithMessage(ctx, turnID, calls, "turn cancelled")
}

// writeToolResultsWithMessage 是 writeCancelledToolResults 的
// 共享实现，用指定消息文本写出 tool_result，保证 LLM 协议平衡。
func (e *turnEngine) writeToolResultsWithMessage(ctx actor.Context, turnID string, calls []pendingToolCall, msg string) {
	if e.openToolCalls > 0 {
		e.openToolCalls -= len(calls)
		if e.openToolCalls < 0 {
			e.openToolCalls = 0
		}
	}
	for _, call := range calls {
		// 1. 把 tool_result 块附加到该 tool_use 对应的 tool_call step 上，
		//    并 emit block.appended + step.error，让 a.steps 与 e.delta 一致。
		for i := range e.batchSteps {
			if e.batchSteps[i].ToolUseID != call.ID {
				continue
			}
			step := e.batchSteps[i]
			step.Output = msg
			step.Text = msg
			step.State = "failed"
			e.upsertStep(step)
			e.emitStepEvent(domain.StepEvent{
				Kind:   "block.appended",
				StepID: step.ID,
				TurnID: turnID,
				Block: &domain.ContentBlock{
					Type:      domain.ContentBlockToolResult,
					ToolUseID: call.ID,
					Text:      msg,
					IsError:   true,
				},
			})
			e.emitStepEvent(domain.StepEvent{
				Kind:   "step.error",
				StepID: step.ID,
				TurnID: turnID,
				Error:  msg,
			})
			break
		}
		// 2. 追加 tool_result 到 history/delta，保证 LLM 协议平衡。
		e.appendMessage(domain.ChatMessage{
			ID:   ctx.NewID().String(),
			Role: "tool",
			Content: []domain.ContentBlock{{
				Type:      domain.ContentBlockToolResult,
				ToolUseID: call.ID,
				Text:      msg,
				IsError:   true,
			}},
		})
	}
}

// advanceBatch 进入下一个分区 batch 并回到 Audit。
func (e *turnEngine) advanceBatch(ctx actor.Context, turnID string) {
	e.batchIdx++
	e.setLoopState(ctx, turnID, LoopAudit)
}

// buildDeniedResults 为被拒绝 batch 中的每个调用构造错误结果。
func buildDeniedResults(batch toolExecutionBatch, reason string) []toolExecutionResult {
	results := make([]toolExecutionResult, len(batch.calls))
	for i, call := range batch.calls {
		results[i] = toolExecutionResult{call: call, out: "permission denied: " + reason, isErr: true}
	}
	return results
}

// bypassBatchSummary renders a batch's tool calls as the compact listing shared
// by the bypass approval prompt and its diagnostic report. Per-call input is
// pre-truncated at the sender per the long-field truncation constraint.
// bypassContextSummary 拼装 fast model 裁决所需的 agent 上下文：项目、
// 已配置根目录、是否运行在一次性 worktree。worktree 内的删除属于预期
// 工作流（可从主仓恢复），主仓内的删除不可恢复——fast model 需要这个
// 区分才能逐案裁决而不是按工具形态一刀切。
func (e *turnEngine) bypassContextSummary() string {
	var sb strings.Builder
	sb.WriteString("Agent context:\n")
	proj := e.projectID
	if proj == "" {
		proj = "unknown"
	}
	fmt.Fprintf(&sb, "- Project: %s\n", proj)
	if len(e.roots) > 0 {
		fmt.Fprintf(&sb, "- Project roots: %s\n", strings.Join(e.roots, ", "))
	}
	wt := ""
	if e.worktreeRoot != nil {
		wt = e.worktreeRoot()
	}
	if wt != "" {
		fmt.Fprintf(&sb, "- The agent runs inside a DISPOSABLE git worktree (%s). Files there are a scratch copy: deletions and resets inside the worktree are expected workflow and recoverable from the main repository.\n", wt)
	} else {
		sb.WriteString("- The agent runs in the MAIN repository (no worktree). Deletions and destructive operations there hit the user's live files and are NOT recoverable.\n")
	}
	return sb.String()
}

func bypassBatchSummary(batch toolExecutionBatch) string {
	var sb strings.Builder
	for i, call := range batch.calls {
		input := call.Input
		if len(input) > 2000 {
			input = input[:2000] + " …(truncated)"
		}
		fmt.Fprintf(&sb, "%d. Tool: %s (%s)\n   Input: %s\n", i+1, call.LLMName, call.CallableID, input)
	}
	return sb.String()
}

// executeBypassApproval uses the agent's fast model (via aiaggregator.intent)
// to make a single non-context LLM call that decides whether to auto-approve
// the batch. It returns (allowed, reason). On any failure it denies for safety.
func (e *turnEngine) executeBypassApproval(ctx actor.Context, batch toolExecutionBatch) (bool, string) {
	fastTargets := e.resolveTargets(ctx, e.fastSlot)
	if len(fastTargets) == 0 {
		return false, "bypass: aggregator unavailable"
	}
	fastAgg := fastTargets[0].aggRef
	fastUnit := fastTargets[0].unit

	sys := strings.Replace(agentkit.BypassPermission, "{{CONTEXT}}", e.bypassContextSummary(), 1)
	sys = strings.Replace(sys, "{{TOOL_CALLS}}", bypassBatchSummary(batch), 1)
	intentReq := domain.SendSessionMessageReq{
		System: sys,
		Messages: []domain.ChatMessage{
			{Role: "user", Content: []domain.ContentBlock{{Type: "text", Text: "Decide ALLOW or DENY for the tool calls above."}}},
		},
		AgentID:  e.agentID,
		SlotKind: "fast",
	}
	// Pin the fast-slot unit so the aggregator uses the agent's configured fast
	// model instead of its own (now-removed) IsFast-based selection.
	if fastUnit.Model != "" {
		intentReq.Unit = &fastUnit
	}

	lifecycleCtx, lifecycleCancel := e.lifecycleWithCancel(ctx.Lifecycle())
	defer lifecycleCancel()
	callCtx, cancel := context.WithTimeout(lifecycleCtx, 30*time.Second)
	defer cancel()

	call := fastAgg.Invoke(callCtx, "aiaggregator.intent", intentReq)
	result, err := call.Final(callCtx)
	if err != nil {
		e.logger.Error("turnEngine: bypass approval call failed", "error", err)
		return false, "bypass: intent call failed"
	}

	resp := decodeIntentResult(result)
	e.logger.Info("turnEngine: bypass approval response", "response", resp)

	upper := strings.ToUpper(strings.TrimSpace(resp))
	switch {
	case strings.HasPrefix(upper, "DENY"):
		return false, "bypass denied by fast model"
	case strings.HasPrefix(upper, "ALLOW"):
		return true, "bypass approved by fast model"
	default:
		// Ambiguous response — deny for safety.
		return false, "bypass: ambiguous response"
	}
}

// reportBypassDecision reports the fast model's bypass approval outcome to the
// oracle as a Diagnostic so it surfaces in the frontend Problems panel.
// Called from the bypass branch of phaseExecute (which owns the turnID) for
// both approve and deny outcomes. Failures or a nil hook must not affect the
// batch execution path.
func (e *turnEngine) reportBypassDecision(ctx actor.Context, turnID string, batch toolExecutionBatch, allowed bool, bypassReason string) {
	if e.onReportDiagnostic == nil {
		return
	}
	severity := "warning"
	message := "bypass denied by fast model"
	if bypassReason != "" && bypassReason != "bypass denied by fast model" {
		message += ": " + bypassReason
	}
	if allowed {
		severity = "info"
		message = "bypass approved by fast model"
	}
	callableIDs := make([]string, len(batch.calls))
	for i, call := range batch.calls {
		callableIDs[i] = call.CallableID
	}
	req := domain.OracleReportDiagnosticReq{
		Severity:   severity,
		Source:     "permission_bypass",
		Message:    message,
		AgentID:    e.agentID,
		TurnID:     turnID,
		CallableID: strings.Join(callableIDs, ","),
		Input:      bypassBatchSummary(batch),
	}
	if e.resolveTargets != nil {
		if targets := e.resolveTargets(ctx, e.fastSlot); len(targets) > 0 {
			req.Unit = modelUnitPtrToOracle(&targets[0].unit)
		}
	}
	e.onReportDiagnostic(ctx, req)
}

// trackForkChildren 把成功的 fork_child 调用注册为 pending children，并通知
// 父 agent 跟踪返回的 child ActorID（用于取消、心跳检查和结果路由）。
// Async fork 走 asyncPending：step 立即以 spawn 应答收尾，结果由 agent_wait 收割。
func (e *turnEngine) trackForkChildren(ctx actor.Context, batch toolExecutionBatch, results []toolExecutionResult) {
	for i, call := range batch.calls {
		if !isForkCallable(call.CallableID) || results[i].isErr {
			continue
		}
		stepID := ""
		if i < len(e.batchSteps) {
			stepID = e.batchSteps[i].ID
		}
		child := pendingChild{
			ToolUseID: call.ID,
			StepID:    stepID,
			Kind:      e.forkKindForLLMName(call.LLMName),
			SpawnedAt: time.Now(),
			Async:     call.ForkAsync,
		}
		// The workspace is the spawner; parse its response and register the
		// returned child ActorID in the parent agent's activeChildren map.
		childAgentID := ""
		if results[i].out != "" {
			var resp domain.WorkspaceAgentSpawnByTypeResp
			if err := json.Unmarshal([]byte(results[i].out), &resp); err == nil {
				childAgentID = resp.ChildActorID
			}
		}
		child.AgentID = childAgentID
		if e.onChildTrack != nil && childAgentID != "" {
			e.onChildTrack(ctx, call.ID, childAgentID)
		}
		if child.Async {
			e.asyncPending = append(e.asyncPending, child)
			results[i].out = asyncForkAckOutput(childAgentID, call.LLMName, child.Kind)
			continue
		}
		e.pendingChildren = append(e.pendingChildren, child)
		if stepID != "" {
			e.pendingChildSteps.Store(stepID, struct{}{})
		}
	}
}

// asyncForkAckOutput is the immediate tool_result for an Async=true fork:
// it names the child so the LLM can target it with agent_wait later.
func asyncForkAckOutput(childAgentID, llmName, kind string) string {
	if childAgentID == "" {
		return "async fork spawned (" + llmName + "); spawn succeeded but workspace returned no child actor id — the result will still be delivered via agent_wait (omit AgentIds to wait for all children)"
	}
	return fmt.Sprintf(`{"AsyncSpawned":true,"ChildAgentID":%q,"Tool":"%s","Kind":"%s","NextStep":"call agent_wait (optionally with this ChildAgentID in AgentIds) when you are ready to collect the result; keep working until then"}`, childAgentID, llmName, kind)
}

// findAskUser 返回 batch 中的 ask_user 调用（如有）。
func findAskUser(batch toolExecutionBatch) *pendingToolCall {
	for i, call := range batch.calls {
		if call.CallableID == "ask_user" {
			return &batch.calls[i]
		}
	}
	return nil
}

// executeBlockAskUser handles a batch containing ask_user in an autonomous
// mode (yolo/autopilot): ask_user is blocked (the agent must act autonomously),
// while any sibling calls in the same batch are executed normally.
func (e *turnEngine) executeBlockAskUser(ctx actor.Context, turnID string, askCall *pendingToolCall, batch toolExecutionBatch) error {
	_ = askCall
	e.logger.Info("turnEngine: autonomous mode — blocking ask_user", "turnID", turnID)
	results := make([]toolExecutionResult, len(batch.calls))
	var siblingBatch toolExecutionBatch
	siblingIdx := make([]int, 0, len(batch.calls))
	for i, c := range batch.calls {
		if c.CallableID == "ask_user" {
			results[i] = toolExecutionResult{
				call:  c,
				out:   "ask_user is disabled in autonomous mode. Decide and act autonomously without asking the user.",
				isErr: true,
			}
			continue
		}
		siblingBatch.calls = append(siblingBatch.calls, c)
		siblingIdx = append(siblingIdx, i)
	}
	if len(siblingBatch.calls) > 0 {
		siblingResults := e.executeToolBatch(ctx, siblingBatch)
		for j, idx := range siblingIdx {
			results[idx] = siblingResults[j]
		}
	}
	e.trackForkChildren(ctx, batch, results)
	if err := e.processBatchResults(ctx, turnID, results, e.batchSteps); err != nil {
		return err
	}
	if err := e.flushEvents(ctx); err != nil {
		e.logger.Error("turnEngine: block-ask_user flushEvents failed", "err", err)
	}
	if err := e.flushTurnEvents(ctx); err != nil {
		e.logger.Error("turnEngine: block-ask_user flushTurnEvents failed", "err", err)
	}
	if e.hasSyncPendingChildren() {
		if err := e.executeWaitChildren(ctx, turnID); err != nil {
			return err
		}
	}
	return nil
}

// applyAskUserAnswer 把用户答案作为 tool_result 消息追加到历史，
// 并关闭对应的 batchSteps。
func (e *turnEngine) applyAskUserAnswer(ctx actor.Context, turnID string, askCall *pendingToolCall, answer string) error {
	// Emit interaction resolved before processing.
	stepID := turnID + "-ask-" + askCall.ID
	_ = e.emitStepImmediate(ctx, domain.StepEvent{
		Kind:            "step.interaction_resolved",
		StepID:          stepID,
		TurnID:          turnID,
		InteractionType: "ask_user",
		RequestID:       askCall.ID,
		Task:            map[string]any{"answer": answer},
	})
	e.appendMessage(domain.ChatMessage{
		ID:      ctx.NewID().String(),
		Role:    "tool",
		Content: []domain.ContentBlock{{Type: domain.ContentBlockToolResult, ToolUseID: askCall.ID, Text: answer}},
	})
	for _, toolCallStep := range e.batchSteps {
		toolCallStep.Output = answer
		toolCallStep.Text = answer
		e.emitStepEvent(domain.StepEvent{
			Kind:   "block.appended",
			StepID: toolCallStep.ID,
			TurnID: turnID,
			Block: &domain.ContentBlock{
				Type:      domain.ContentBlockToolResult,
				ToolUseID: askCall.ID,
				Text:      answer,
			},
		})
		toolCallStep.State = "completed"
		e.upsertStep(toolCallStep)
		e.actionCount++
		e.emitStepEvent(domain.StepEvent{
			Kind:   "step.closed",
			StepID: toolCallStep.ID,
			TurnID: turnID,
		})
	}
	return nil
}

// buildToolCallSummaries 把 batch 中的调用转换成用于权限提示的摘要列表。
// MalformedArgs 调用的 Input 已被回退为 "{}"（历史安全），直接展示会误导
// 用户以为批准了一个空参数调用——替换为明确的失败预告。
func buildToolCallSummaries(batch toolExecutionBatch) []domain.ToolCallSummary {
	summaries := make([]domain.ToolCallSummary, len(batch.calls))
	for i, c := range batch.calls {
		input := c.Input
		if c.MalformedArgs {
			input = "(arguments malformed beyond repair — this call will fail without executing)"
		}
		summaries[i] = domain.ToolCallSummary{ID: c.ID, CallableID: c.CallableID, Input: input}
	}
	return summaries
}

// emitPermissionEvent 向前端发送 permission_requested 事件，渲染确认弹窗。
func (e *turnEngine) emitPermissionEvent(ctx actor.Context, turnID, requestID string, summaries []domain.ToolCallSummary) {
	// Emit as step.interaction_requested — frontend renders an interaction card.
	stepID := turnID + "-permission-" + requestID
	toolMaps := make([]map[string]any, len(summaries))
	for i, s := range summaries {
		toolMaps[i] = map[string]any{
			"id":         s.ID,
			"callableId": s.CallableID,
			"input":      s.Input,
		}
		// Registration-permission preview fields (see enrichRegistrationSummaries).
		if s.AppID != "" {
			toolMaps[i]["appId"] = s.AppID
		}
		if s.AppName != "" {
			toolMaps[i]["appName"] = s.AppName
		}
		if len(s.Permissions) > 0 {
			toolMaps[i]["permissions"] = s.Permissions
		}
		if s.PermissionNote != "" {
			toolMaps[i]["permissionNote"] = s.PermissionNote
		}
	}
	payload := map[string]any{
		"toolCalls": toolMaps,
		"reason":    e.permissionReason,
	}
	if err := e.emitStepImmediate(ctx, domain.StepEvent{
		Kind:            "step.interaction_requested",
		StepID:          stepID,
		TurnID:          turnID,
		InteractionType: "permission",
		RequestID:       requestID,
		Task:            payload,
	}); err != nil {
		e.logger.Warn("turnEngine: failed to emit step.interaction_requested (permission)", "error", err)
	} else if e.onPermissionRequested != nil {
		e.onPermissionRequested()
	}
}

// confirmFileMutatingCalls sets Confirm=true on file mutating tool inputs so
// that actor-level outside-root checks allow the approved operation.
func confirmFileMutatingCalls(batch toolExecutionBatch) {
	for i := range batch.calls {
		call := &batch.calls[i]
		if !fileMutatingCallables[call.CallableID] {
			continue
		}
		var input map[string]interface{}
		if err := json.Unmarshal([]byte(call.Input), &input); err != nil {
			continue
		}
		input["Confirm"] = true
		b, err := json.Marshal(input)
		if err != nil {
			continue
		}
		call.Input = string(b)
	}
}

// applyPermissionAnswer 根据用户权限答案执行 batch 或拒绝它。
func (e *turnEngine) applyPermissionAnswer(ctx actor.Context, turnID, requestID string, batch toolExecutionBatch, answer string) error {
	var ans struct {
		Allowed        bool `json:"allowed"`
		AllowInProject bool `json:"allowInProject"`
	}
	_ = json.Unmarshal([]byte(answer), &ans)
	// Emit resolution event.
	stepID := turnID + "-permission-" + requestID
	_ = e.emitStepImmediate(ctx, domain.StepEvent{
		Kind:            "step.interaction_resolved",
		StepID:          stepID,
		TurnID:          turnID,
		InteractionType: "permission",
		RequestID:       requestID,
		Task:            map[string]any{"allowed": ans.Allowed, "answer": answer},
	})
	if ans.Allowed {
		if ans.AllowInProject && e.onGrantPermission != nil && e.projectID != "" {
			for _, call := range batch.calls {
				// Registration consent must be re-confirmed on every
				// registration: persisting it as a project-level grant would
				// let future registration callables auto-approve, defeating
				// the forced interception in checkBatchPermission.
				if isRegistrationCallable(call.CallableID) {
					continue
				}
				e.onGrantPermission(e.projectID, call.CallableID)
			}
		}
		results := e.executeToolBatch(ctx, batch)
		return e.processBatchResults(ctx, turnID, results, e.batchSteps)
	}
	return e.processBatchResults(ctx, turnID, buildDeniedResults(batch, ""), e.batchSteps)
}

// findPlanSubmit returns the index of the plan.submit call in the batch, or -1.
func findPlanSubmit(batch toolExecutionBatch) int {
	for i, call := range batch.calls {
		if call.CallableID == "plan_submit" {
			return i
		}
	}
	return -1
}

// findGoalSubmit returns the index of the goal.submit call in the batch, or -1.
func findGoalSubmit(batch toolExecutionBatch) int {
	for i, call := range batch.calls {
		if call.CallableID == "goal_submit" {
			return i
		}
	}
	return -1
}

// findWorkflowStart returns the index of the workflow_start call in the batch, or -1.
func findWorkflowStart(batch toolExecutionBatch) int {
	for i, call := range batch.calls {
		if call.CallableID == "workflow_start" {
			return i
		}
	}
	return -1
}

// findWorkflowPlanSubmit returns the index of the workflow_plan_submit call in
// the batch, or -1.
func findWorkflowPlanSubmit(batch toolExecutionBatch) int {
	for i, call := range batch.calls {
		if call.CallableID == "workflow_plan_submit" {
			return i
		}
	}
	return -1
}

// findGoalCardSubmit returns the index of the goal_card_submit call in the
// batch, or -1.
func findGoalCardSubmit(batch toolExecutionBatch) int {
	for i, call := range batch.calls {
		if call.CallableID == "goal_card_submit" {
			return i
		}
	}
	return -1
}

// executeWaitPlanApproval submits the plan, emits an approval event, and blocks
// on resumeCh until the user approves, edits, or rejects the plan.
func (e *turnEngine) executeWaitPlanApproval(ctx actor.Context, turnID string, planCall *pendingToolCall) error {
	e.logger.Info("turnEngine: plan.submit detected, submitting plan for approval", "turnID", turnID)
	requestID, err := e.onPlanSubmit(ctx, planCall.Input)
	if err != nil {
		// onPlanSubmit failures (plan already pending, JSON parse error, empty
		// plan fallback) are recoverable: close the plan_call step as failed,
		// surface the error as a tool_result so the LLM can adapt, and let the
		// turn keep going. Returning err here leaves the step in "running" and
		// closeRemainingOpenSteps later mislabels it as "turn ended while step
		// was still open".
		e.logger.Error("turnEngine: plan.submit submission failed", "error", err)
		e.closePlanStepWithError(ctx, turnID, planCall, err.Error())
		if e.onReportDiagnostic != nil {
			e.onReportDiagnostic(ctx, domain.OracleReportDiagnosticReq{
				Severity:   "error",
				Source:     "plan_submit",
				Message:    err.Error(),
				TurnID:     turnID,
				CallableID: "plan_submit",
				ToolUseID:  planCall.ID,
				Input:      truncateForDiagnostic(planCall.Input),
				RawData:    planCall.RawToolCall,
				Unit:       modelUnitPtrToOracle(e.startReq.Input.Unit),
			})
		}
		return nil
	}
	// 自主模式（yolo/autopilot）：自动批准计划，不弹出审批卡片、不等待用户。
	if e.isAutonomousMode() {
		e.logger.Info("turnEngine: autonomous mode — auto-approving plan", "turnID", turnID, "requestID", requestID)
		if err := e.applyPlanApprovalAnswer(ctx, turnID, planCall, `{"decision":"approve"}`); err != nil {
			return err
		}
		if err := e.flushEvents(ctx); err != nil {
			e.logger.Error("turnEngine: yolo plan auto-approve flushEvents failed", "err", err)
		}
		if err := e.flushTurnEvents(ctx); err != nil {
			e.logger.Error("turnEngine: yolo plan auto-approve flushTurnEvents failed", "err", err)
		}
		return nil
	}
	e.resumeRequestID = requestID
	e.logger.Info("turnEngine: emitting plan_approval_requested", "turnID", turnID, "requestID", requestID)
	e.onPlanSubmitted(ctx, turnID, requestID)

	// Flush events so the frontend receives the approval prompt immediately.
	if err := e.flushEvents(ctx); err != nil {
		e.logger.Error("turnEngine: plan approval flushEvents failed", "err", err)
	}
	if err := e.flushTurnEvents(ctx); err != nil {
		e.logger.Error("turnEngine: plan approval flushTurnEvents failed", "err", err)
	}

	e.logger.Info("turnEngine: waiting for plan approval answer", "turnID", turnID, "requestID", requestID)
	select {
	case <-e.resumeCh:
		answer := e.resumeAnswer
		e.resumeAnswer = ""
		e.resumeRequestID = ""
		e.logger.Info("turnEngine: plan approval answer received", "turnID", turnID, "answer", answer)
		return e.applyPlanApprovalAnswer(ctx, turnID, planCall, answer)
		// NOTE: no pauseDone case — see executeWaitAskUser. A pending plan
		// approval is the user-input gate and must not be abandoned by a
		// pause; keep waiting for the user's approve/reject answer.
	case <-e.done:
		// Plan was pending approval when the turn got cancelled. Drop the
		// pending state so a fresh plan_submit can succeed; otherwise the
		// agent would lock future plans with "plan already submitted".
		// Done before writeCancelledToolResults so the plan.submit step
		// closure stays the cancellation's responsibility (no event race).
		if e.onPlanExpire != nil && e.resumeRequestID != "" {
			e.onPlanExpire(ctx, e.resumeRequestID)
		}
		e.resumeRequestID = ""
		e.writeCancelledToolResults(ctx, turnID, []pendingToolCall{*planCall})
		return context.Canceled
	case <-ctx.Lifecycle().Done():
		if e.onPlanExpire != nil && e.resumeRequestID != "" {
			e.onPlanExpire(ctx, e.resumeRequestID)
		}
		e.resumeRequestID = ""
		e.writeCancelledToolResults(ctx, turnID, []pendingToolCall{*planCall})
		return context.Canceled
	}
}

// applyPlanApprovalAnswer records the user's plan approval decision as a
// tool_result, runs the plan state mutation callback (on the exec loop),
// injects synthetic task-creation messages, and closes the plan.submit step.
func (e *turnEngine) applyPlanApprovalAnswer(ctx actor.Context, turnID string, planCall *pendingToolCall, answer string) error {
	var resolvedDecision, resolvedRequestID, resolvedFeedback string
	var resolvedTasks []gen.Task
	if e.onPlanResolveApproval != nil {
		resolvedDecision, resolvedTasks, resolvedRequestID, resolvedFeedback = e.onPlanResolveApproval(ctx, answer)
	}

	stepID := turnID + "-plan-" + resolvedRequestID
	_ = e.emitStepImmediate(ctx, domain.StepEvent{
		Kind:            "step.interaction_resolved",
		StepID:          stepID,
		TurnID:          turnID,
		InteractionType: "plan_approval",
		RequestID:       resolvedRequestID,
		Task:            map[string]any{"decision": resolvedDecision, "answer": answer},
	})

	var resultText string
	switch resolvedDecision {
	case "approve", "edit":
		resultText = "Plan approved. Proceed with execution."
	case "confirm_goal":
		resultText = "Plan confirmed as goal. Proceed with autonomous goal-driven execution."
	case "start_workflow":
		resultText = "Plan converted to workflow. Workflow mode is now active and the map card is bound to you.\n" +
			"Do NOT execute the plan tasks yourself. Your role is now orchestrator, not executor.\n" +
			"Next steps:\n" +
			"1. Create task cards from the plan's tasks via project.wiki_create_task_card, wiring depends_on edges.\n" +
			"2. Read the frontier with project.wiki_frontier to find unblocked tasks.\n" +
			"3. Spawn workers for frontier tasks via workspace.agent_spawn_assign (one worker per task card).\n" +
			"4. Review completed workers with workspace.agent_review, then drain the next frontier round.\n" +
			"When the overall goal is met and all workers are resolved, call workflow_stop."
	case "reject":
		if resolvedFeedback != "" {
			resultText = "Plan rejected. User feedback: " + resolvedFeedback
		} else {
			resultText = "Plan rejected. Please revise and submit again."
		}
	default:
		resultText = "Unknown approval decision."
	}
	ctx.Logger().Info("agent: plan approval result text", "resultText", resultText, "decision", resolvedDecision, "feedback", resolvedFeedback)

	if e.openToolCalls > 0 {
		e.openToolCalls--
	}

	e.appendMessage(domain.ChatMessage{
		ID:      ctx.NewID().String(),
		Role:    "tool",
		Content: []domain.ContentBlock{{Type: domain.ContentBlockToolResult, ToolUseID: planCall.ID, Text: resultText}},
	})

	if resolvedDecision == "approve" || resolvedDecision == "edit" {
		for _, task := range resolvedTasks {
			e.appendSyntheticTaskCreation(ctx, task)
		}
	}

	var planStep *domain.TurnAction
	for i := range e.batchSteps {
		if e.batchSteps[i].ToolUseID == planCall.ID {
			step := e.batchSteps[i]
			planStep = &step
			break
		}
	}
	if planStep == nil {
		// Should be unreachable — phaseAudit always opens the plan_call step
		// before we get here. Don't fail the turn if it ever happens: the
		// tool_result is already appended above, so protocol balance holds.
		// Failing here would strand a "running" step and trip the generic
		// "turn ended while step was still open" sweep.
		e.logger.Warn("turnEngine: plan.submit step not found in batchSteps; tool_result already written, skipping step closure", "toolUseID", planCall.ID)
		return nil
	}

	planStep.Output = resultText
	planStep.Text = resultText
	planStep.State = "completed"
	e.upsertStep(*planStep)
	e.actionCount++
	e.emitStepEvent(domain.StepEvent{
		Kind:   "block.appended",
		StepID: planStep.ID,
		TurnID: turnID,
		Block: &domain.ContentBlock{
			Type:      domain.ContentBlockToolResult,
			ToolUseID: planCall.ID,
			Text:      resultText,
		},
	})
	e.emitStepEvent(domain.StepEvent{
		Kind:   "step.closed",
		StepID: planStep.ID,
		TurnID: turnID,
	})
	return nil
}

// executeWaitGoalSubmit submits the goal interpretation, emits a confirmation
// event, and blocks on resumeCh until the user approves or rejects.
func (e *turnEngine) executeWaitGoalSubmit(ctx actor.Context, turnID string, goalCall *pendingToolCall) error {
	e.logger.Info("turnEngine: goal.submit detected, submitting goal for confirmation", "turnID", turnID)
	requestID, err := e.onGoalSubmit(ctx, goalCall.Input)
	if err != nil {
		e.logger.Error("turnEngine: goal.submit submission failed", "error", err)
		e.closePlanStepWithError(ctx, turnID, goalCall, err.Error())
		if e.onReportDiagnostic != nil {
			e.onReportDiagnostic(ctx, domain.OracleReportDiagnosticReq{
				Severity:   "error",
				Source:     "goal_submit",
				Message:    err.Error(),
				TurnID:     turnID,
				CallableID: "goal_submit",
				Unit:       modelUnitPtrToOracle(e.startReq.Input.Unit),
			})
		}
		return nil
	}
	// 自主模式（yolo/autopilot）：自动确认目标，不弹出确认卡片、不等待用户。
	if e.isAutonomousMode() {
		e.logger.Info("turnEngine: autonomous mode — auto-approving goal", "turnID", turnID, "requestID", requestID)
		if err := e.applyGoalSubmitAnswer(ctx, turnID, goalCall, `{"decision":"approve"}`); err != nil {
			return err
		}
		if err := e.flushEvents(ctx); err != nil {
			e.logger.Error("turnEngine: yolo goal auto-approve flushEvents failed", "err", err)
		}
		if err := e.flushTurnEvents(ctx); err != nil {
			e.logger.Error("turnEngine: yolo goal auto-approve flushTurnEvents failed", "err", err)
		}
		return nil
	}
	e.resumeRequestID = requestID
	e.logger.Info("turnEngine: emitting goal_submit_requested", "turnID", turnID, "requestID", requestID)
	e.onGoalSubmitted(ctx, turnID, requestID)

	if err := e.flushEvents(ctx); err != nil {
		e.logger.Error("turnEngine: goal_submit flushEvents failed", "err", err)
	}
	if err := e.flushTurnEvents(ctx); err != nil {
		e.logger.Error("turnEngine: goal_submit flushTurnEvents failed", "err", err)
	}

	e.logger.Info("turnEngine: waiting for goal_submit answer", "turnID", turnID, "requestID", requestID)
	select {
	case <-e.resumeCh:
		answer := e.resumeAnswer
		e.resumeAnswer = ""
		e.resumeRequestID = ""
		e.logger.Info("turnEngine: goal_submit answer received", "turnID", turnID, "answer", answer)
		return e.applyGoalSubmitAnswer(ctx, turnID, goalCall, answer)
		// NOTE: no pauseDone case — see executeWaitAskUser. A pending goal
		// submit is the user-input gate and must not be abandoned by a pause;
		// keep waiting for the user's confirm/revise answer.
	case <-e.done:
		if e.onGoalExpire != nil && e.resumeRequestID != "" {
			e.onGoalExpire(ctx, e.resumeRequestID)
		}
		e.resumeRequestID = ""
		e.writeCancelledToolResults(ctx, turnID, []pendingToolCall{*goalCall})
		return context.Canceled
	case <-ctx.Lifecycle().Done():
		if e.onGoalExpire != nil && e.resumeRequestID != "" {
			e.onGoalExpire(ctx, e.resumeRequestID)
		}
		e.resumeRequestID = ""
		e.writeCancelledToolResults(ctx, turnID, []pendingToolCall{*goalCall})
		return context.Canceled
	}
}

// applyGoalSubmitAnswer records the user's goal_submit decision as a tool_result
// and closes the goal.submit step.
func (e *turnEngine) applyGoalSubmitAnswer(ctx actor.Context, turnID string, goalCall *pendingToolCall, answer string) error {
	var resolvedDecision, resolvedRequestID, resolvedFeedback string
	if e.onGoalResolveSubmit != nil {
		resolvedDecision, resolvedRequestID, resolvedFeedback = e.onGoalResolveSubmit(ctx, answer)
	}

	stepID := turnID + "-goal-submit-" + resolvedRequestID
	_ = e.emitStepImmediate(ctx, domain.StepEvent{
		Kind:            "step.interaction_resolved",
		StepID:          stepID,
		TurnID:          turnID,
		InteractionType: "goal_submit",
		RequestID:       resolvedRequestID,
		Task:            map[string]any{"decision": resolvedDecision, "answer": answer},
	})

	var resultText string
	switch resolvedDecision {
	case "approve", "edit":
		resultText = "Goal confirmed. Proceed with execution toward the confirmed goal."
	case "reject":
		if resolvedFeedback != "" {
			resultText = "Goal interpretation rejected. User feedback: " + resolvedFeedback + " Please revise your understanding and call goal_submit again."
		} else {
			resultText = "Goal interpretation rejected. Please revise your understanding and call goal_submit again."
		}
	default:
		resultText = "Unknown goal_submit decision."
	}
	ctx.Logger().Info("agent: goal_submit result text", "resultText", resultText, "decision", resolvedDecision, "feedback", resolvedFeedback)

	if e.openToolCalls > 0 {
		e.openToolCalls--
	}

	e.appendMessage(domain.ChatMessage{
		ID:      ctx.NewID().String(),
		Role:    "tool",
		Content: []domain.ContentBlock{{Type: domain.ContentBlockToolResult, ToolUseID: goalCall.ID, Text: resultText}},
	})

	var goalStep *domain.TurnAction
	for i := range e.batchSteps {
		if e.batchSteps[i].ToolUseID == goalCall.ID {
			step := e.batchSteps[i]
			goalStep = &step
			break
		}
	}
	if goalStep == nil {
		e.logger.Warn("turnEngine: goal.submit step not found in batchSteps; tool_result already written, skipping step closure", "toolUseID", goalCall.ID)
		return nil
	}

	goalStep.Output = resultText
	goalStep.Text = resultText
	goalStep.State = "completed"
	e.upsertStep(*goalStep)
	e.actionCount++
	e.emitStepEvent(domain.StepEvent{
		Kind:   "block.appended",
		StepID: goalStep.ID,
		TurnID: turnID,
		Block: &domain.ContentBlock{
			Type:      domain.ContentBlockToolResult,
			ToolUseID: goalCall.ID,
			Text:      resultText,
		},
	})
	e.emitStepEvent(domain.StepEvent{
		Kind:   "step.closed",
		StepID: goalStep.ID,
		TurnID: turnID,
	})
	return nil
}

// executeWaitWorkflowStart submits the workflow map start request, derives an
// InterpretedGoal, emits a goal_submit-style confirmation event, and blocks on
// resumeCh until the user approves or rejects. On approval it activates the
// workflow map and persists the active workflow state.
func (e *turnEngine) executeWaitWorkflowStart(ctx actor.Context, turnID string, workflowCall *pendingToolCall) error {
	return e.executeWorkflowConfirmation(ctx, turnID, workflowCall, e.onWorkflowStart, "workflow_start")
}

// executeWaitWorkflowPlanSubmit creates the plan card + workflow map, then
// enters the same two-phase workflow confirmation as workflow_start.
func (e *turnEngine) executeWaitWorkflowPlanSubmit(ctx actor.Context, turnID string, planCall *pendingToolCall) error {
	return e.executeWorkflowConfirmation(ctx, turnID, planCall, e.onWorkflowPlanSubmit, "workflow_plan_submit")
}

// executeWorkflowConfirmation is the shared confirmation flow for workflow_start
// and workflow_plan_submit. It calls submitFn to derive/store the pending
// confirmation, emits the interaction event, blocks on resumeCh for the user's
// answer, and delegates resolution to applyWorkflowStartAnswer.
func (e *turnEngine) executeWorkflowConfirmation(ctx actor.Context, turnID string, call *pendingToolCall, submitFn func(actor.Context, string) (string, error), source string) error {
	e.logger.Info("turnEngine: workflow confirmation detected", "source", source, "turnID", turnID)
	requestID, err := submitFn(ctx, call.Input)
	if err != nil {
		e.logger.Error("turnEngine: workflow submission failed", "source", source, "error", err)
		e.closePlanStepWithError(ctx, turnID, call, err.Error())
		if e.onReportDiagnostic != nil {
			e.onReportDiagnostic(ctx, domain.OracleReportDiagnosticReq{
				Severity:   "error",
				Source:     source,
				Message:    err.Error(),
				TurnID:     turnID,
				CallableID: source,
				Unit:       modelUnitPtrToOracle(e.startReq.Input.Unit),
			})
		}
		return nil
	}
	// 自主模式（yolo/autopilot）：自动批准 workflow start，不等待用户确认。
	if e.isAutonomousMode() {
		e.logger.Info("turnEngine: autonomous mode — auto-approving", "source", source, "turnID", turnID, "requestID", requestID)
		if err := e.applyWorkflowStartAnswer(ctx, turnID, call, `{"decision":"approve"}`); err != nil {
			return err
		}
		if err := e.flushEvents(ctx); err != nil {
			e.logger.Error("turnEngine: yolo auto-approve flushEvents failed", "source", source, "err", err)
		}
		if err := e.flushTurnEvents(ctx); err != nil {
			e.logger.Error("turnEngine: yolo auto-approve flushTurnEvents failed", "source", source, "err", err)
		}
		return nil
	}
	e.resumeRequestID = requestID
	e.logger.Info("turnEngine: emitting confirmation request", "source", source, "turnID", turnID, "requestID", requestID)
	e.onWorkflowStartSubmitted(ctx, turnID, requestID)

	if err := e.flushEvents(ctx); err != nil {
		e.logger.Error("turnEngine: confirmation flushEvents failed", "source", source, "err", err)
	}
	if err := e.flushTurnEvents(ctx); err != nil {
		e.logger.Error("turnEngine: confirmation flushTurnEvents failed", "source", source, "err", err)
	}

	e.logger.Info("turnEngine: waiting for answer", "source", source, "turnID", turnID, "requestID", requestID)
	select {
	case <-e.resumeCh:
		answer := e.resumeAnswer
		e.resumeAnswer = ""
		e.resumeRequestID = ""
		e.logger.Info("turnEngine: answer received", "source", source, "turnID", turnID, "answer", answer)
		return e.applyWorkflowStartAnswer(ctx, turnID, call, answer)
	case <-e.done:
		if e.onWorkflowStartExpire != nil && e.resumeRequestID != "" {
			e.onWorkflowStartExpire(ctx, e.resumeRequestID)
		}
		e.resumeRequestID = ""
		e.writeCancelledToolResults(ctx, turnID, []pendingToolCall{*call})
		return context.Canceled
	case <-ctx.Lifecycle().Done():
		if e.onWorkflowStartExpire != nil && e.resumeRequestID != "" {
			e.onWorkflowStartExpire(ctx, e.resumeRequestID)
		}
		e.resumeRequestID = ""
		e.writeCancelledToolResults(ctx, turnID, []pendingToolCall{*call})
		return context.Canceled
	}
}

// applyWorkflowStartAnswer records the user's workflow_start decision as a
// tool_result and closes the workflow_start step. On approval it activates the
// workflow map; on rejection it leaves the workflow inactive.
func (e *turnEngine) applyWorkflowStartAnswer(ctx actor.Context, turnID string, workflowCall *pendingToolCall, answer string) error {
	var resolvedDecision, resolvedRequestID, resolvedFeedback string
	if e.onWorkflowStartResolveSubmit != nil {
		resolvedDecision, resolvedRequestID, resolvedFeedback = e.onWorkflowStartResolveSubmit(ctx, answer)
	}

	stepID := turnID + "-workflow-start-" + resolvedRequestID
	_ = e.emitStepImmediate(ctx, domain.StepEvent{
		Kind:            "step.interaction_resolved",
		StepID:          stepID,
		TurnID:          turnID,
		InteractionType: "goal_submit",
		RequestID:       resolvedRequestID,
		Task:            map[string]any{"decision": resolvedDecision, "answer": answer},
	})

	var resultText string
	switch resolvedDecision {
	case "approve", "edit":
		resultText = "Workflow started. The map is now active and can be orchestrated."
	case "reject":
		if resolvedFeedback != "" {
			resultText = "Workflow start rejected. User feedback: " + resolvedFeedback + " Please revise and call workflow_start again if you still want to start this map."
		} else {
			resultText = "Workflow start rejected. Please revise and call workflow_start again if you still want to start this map."
		}
	default:
		resultText = "Unknown workflow_start decision."
	}
	ctx.Logger().Info("agent: workflow_start result text", "resultText", resultText, "decision", resolvedDecision, "feedback", resolvedFeedback)

	if e.openToolCalls > 0 {
		e.openToolCalls--
	}

	e.appendMessage(domain.ChatMessage{
		ID:      ctx.NewID().String(),
		Role:    "tool",
		Content: []domain.ContentBlock{{Type: domain.ContentBlockToolResult, ToolUseID: workflowCall.ID, Text: resultText}},
	})

	var workflowStep *domain.TurnAction
	for i := range e.batchSteps {
		if e.batchSteps[i].ToolUseID == workflowCall.ID {
			step := e.batchSteps[i]
			workflowStep = &step
			break
		}
	}
	if workflowStep == nil {
		e.logger.Warn("turnEngine: workflow_start step not found in batchSteps; tool_result already written, skipping step closure", "toolUseID", workflowCall.ID)
		return nil
	}

	workflowStep.Output = resultText
	workflowStep.Text = resultText
	workflowStep.State = "completed"
	e.upsertStep(*workflowStep)
	e.actionCount++
	e.emitStepEvent(domain.StepEvent{
		Kind:   "block.appended",
		StepID: workflowStep.ID,
		TurnID: turnID,
		Block: &domain.ContentBlock{
			Type:      domain.ContentBlockToolResult,
			ToolUseID: workflowCall.ID,
			Text:      resultText,
		},
	})
	e.emitStepEvent(domain.StepEvent{
		Kind:   "step.closed",
		StepID: workflowStep.ID,
		TurnID: turnID,
	})
	return nil
}

// executeWaitGoalCardSubmit submits the task-card proposal, emits a
// confirmation event, and blocks on resumeCh until the user approves or
// rejects. Mirrors executeWaitGoalSubmit for goal.submit.
func (e *turnEngine) executeWaitGoalCardSubmit(ctx actor.Context, turnID string, cardCall *pendingToolCall) error {
	e.logger.Info("turnEngine: goal_card_submit detected, submitting task-card proposal for confirmation", "turnID", turnID)
	requestID, err := e.onGoalCardSubmit(ctx, cardCall.Input)
	if err != nil {
		e.logger.Error("turnEngine: goal_card_submit submission failed", "error", err)
		e.closePlanStepWithError(ctx, turnID, cardCall, err.Error())
		if e.onReportDiagnostic != nil {
			e.onReportDiagnostic(ctx, domain.OracleReportDiagnosticReq{
				Severity:   "error",
				Source:     "goal_card_submit",
				Message:    err.Error(),
				TurnID:     turnID,
				CallableID: "goal_card_submit",
				Unit:       modelUnitPtrToOracle(e.startReq.Input.Unit),
			})
		}
		return nil
	}
	// 自主模式（yolo/autopilot）：自动确认任务卡，不弹出确认卡片、不等待用户。
	if e.isAutonomousMode() {
		e.logger.Info("turnEngine: autonomous mode — auto-approving goal card", "turnID", turnID, "requestID", requestID)
		if err := e.applyGoalCardSubmitAnswer(ctx, turnID, cardCall, `{"decision":"approve"}`); err != nil {
			return err
		}
		if err := e.flushEvents(ctx); err != nil {
			e.logger.Error("turnEngine: yolo goal card auto-approve flushEvents failed", "err", err)
		}
		if err := e.flushTurnEvents(ctx); err != nil {
			e.logger.Error("turnEngine: yolo goal card auto-approve flushTurnEvents failed", "err", err)
		}
		return nil
	}
	e.resumeRequestID = requestID
	e.logger.Info("turnEngine: emitting goal_card_submit_requested", "turnID", turnID, "requestID", requestID)
	e.onGoalCardSubmitted(ctx, turnID, requestID)

	if err := e.flushEvents(ctx); err != nil {
		e.logger.Error("turnEngine: goal_card_submit flushEvents failed", "err", err)
	}
	if err := e.flushTurnEvents(ctx); err != nil {
		e.logger.Error("turnEngine: goal_card_submit flushTurnEvents failed", "err", err)
	}

	e.logger.Info("turnEngine: waiting for goal_card_submit answer", "turnID", turnID, "requestID", requestID)
	select {
	case <-e.resumeCh:
		answer := e.resumeAnswer
		e.resumeAnswer = ""
		e.resumeRequestID = ""
		e.logger.Info("turnEngine: goal_card_submit answer received", "turnID", turnID, "answer", answer)
		return e.applyGoalCardSubmitAnswer(ctx, turnID, cardCall, answer)
		// NOTE: no pauseDone case — see executeWaitAskUser. A pending goal
		// card submit is the user-input gate and must not be abandoned by a
		// pause; keep waiting for the user's confirm/revise answer.
	case <-e.done:
		if e.onGoalCardExpire != nil && e.resumeRequestID != "" {
			e.onGoalCardExpire(ctx, e.resumeRequestID)
		}
		e.resumeRequestID = ""
		e.writeCancelledToolResults(ctx, turnID, []pendingToolCall{*cardCall})
		return context.Canceled
	case <-ctx.Lifecycle().Done():
		if e.onGoalCardExpire != nil && e.resumeRequestID != "" {
			e.onGoalCardExpire(ctx, e.resumeRequestID)
		}
		e.resumeRequestID = ""
		e.writeCancelledToolResults(ctx, turnID, []pendingToolCall{*cardCall})
		return context.Canceled
	}
}

// applyGoalCardSubmitAnswer records the user's goal_card_submit decision as a
// tool_result and closes the goal_card_submit step. On approval the resolved
// task card ID (or the creation error) is surfaced to the LLM so it knows its
// binding and can adapt when card creation failed.
func (e *turnEngine) applyGoalCardSubmitAnswer(ctx actor.Context, turnID string, cardCall *pendingToolCall, answer string) error {
	var resolvedDecision, resolvedRequestID, resolvedFeedback, resolvedCardID string
	var resolveErr error
	if e.onGoalCardResolveSubmit != nil {
		resolvedDecision, resolvedRequestID, resolvedFeedback, resolvedCardID, resolveErr = e.onGoalCardResolveSubmit(ctx, answer)
	}

	stepID := turnID + "-goal-card-submit-" + resolvedRequestID
	_ = e.emitStepImmediate(ctx, domain.StepEvent{
		Kind:            "step.interaction_resolved",
		StepID:          stepID,
		TurnID:          turnID,
		InteractionType: "goal_card_submit",
		RequestID:       resolvedRequestID,
		Task:            map[string]any{"decision": resolvedDecision, "answer": answer},
	})

	var resultText string
	switch {
	case resolveErr != nil:
		resultText = "Task card creation failed: " + resolveErr.Error() + " You may call goal_card_submit again with a revised proposal."
	case resolvedDecision == "approve" || resolvedDecision == "edit":
		if resolvedCardID != "" {
			resultText = "Task card created and bound. You are now executing the confirmed goal bound to task card " + resolvedCardID + "."
		} else {
			resultText = "Goal card approved, but no task card was created."
		}
	case resolvedDecision == "reject":
		if resolvedFeedback != "" {
			resultText = "Task card proposal rejected. User feedback: " + resolvedFeedback + " Please revise your understanding and call goal_card_submit again."
		} else {
			resultText = "Task card proposal rejected. Please revise your understanding and call goal_card_submit again."
		}
	default:
		resultText = "Unknown goal_card_submit decision."
	}
	ctx.Logger().Info("agent: goal_card_submit result text", "resultText", resultText, "decision", resolvedDecision, "feedback", resolvedFeedback, "cardID", resolvedCardID)

	if e.openToolCalls > 0 {
		e.openToolCalls--
	}

	e.appendMessage(domain.ChatMessage{
		ID:      ctx.NewID().String(),
		Role:    "tool",
		Content: []domain.ContentBlock{{Type: domain.ContentBlockToolResult, ToolUseID: cardCall.ID, Text: resultText}},
	})

	var cardStep *domain.TurnAction
	for i := range e.batchSteps {
		if e.batchSteps[i].ToolUseID == cardCall.ID {
			step := e.batchSteps[i]
			cardStep = &step
			break
		}
	}
	if cardStep == nil {
		e.logger.Warn("turnEngine: goal_card_submit step not found in batchSteps; tool_result already written, skipping step closure", "toolUseID", cardCall.ID)
		return nil
	}

	cardStep.Output = resultText
	cardStep.Text = resultText
	cardStep.State = "completed"
	e.upsertStep(*cardStep)
	e.actionCount++
	e.emitStepEvent(domain.StepEvent{
		Kind:   "block.appended",
		StepID: cardStep.ID,
		TurnID: turnID,
		Block: &domain.ContentBlock{
			Type:      domain.ContentBlockToolResult,
			ToolUseID: cardCall.ID,
			Text:      resultText,
		},
	})
	e.emitStepEvent(domain.StepEvent{
		Kind:   "step.closed",
		StepID: cardStep.ID,
		TurnID: turnID,
	})
	return nil
}

// closePlanStepWithError closes the plan.submit tool_call step as failed and
// writes a tool_result with IsError=true. Use it whenever plan.submit's
// upstream submission fails (e.g. plan already pending, JSON parse error,
// empty plan fallback) so the LLM sees the error as a normal tool_result,
// adapts, and the turn keeps going — instead of the whole turn failing and
// closeRemainingOpenSteps belatedly emitting "turn ended while step was still
// open". Mirrors writeCancelledToolResults' single-call path but with a
// caller-supplied message.
func (e *turnEngine) closePlanStepWithError(ctx actor.Context, turnID string, planCall *pendingToolCall, errMsg string) {
	if e.openToolCalls > 0 {
		e.openToolCalls--
		if e.openToolCalls < 0 {
			e.openToolCalls = 0
		}
	}
	for i := range e.batchSteps {
		if e.batchSteps[i].ToolUseID != planCall.ID {
			continue
		}
		step := e.batchSteps[i]
		step.Output = errMsg
		step.Text = errMsg
		step.Error = errMsg
		step.State = "failed"
		e.upsertStep(step)
		e.emitStepEvent(domain.StepEvent{
			Kind:   "block.appended",
			StepID: step.ID,
			TurnID: turnID,
			Block: &domain.ContentBlock{
				Type:      domain.ContentBlockToolResult,
				ToolUseID: planCall.ID,
				Text:      errMsg,
				IsError:   true,
			},
		})
		e.emitStepEvent(domain.StepEvent{
			Kind:   "step.error",
			StepID: step.ID,
			TurnID: turnID,
			Error:  errMsg,
		})
		break
	}
	e.appendMessage(domain.ChatMessage{
		ID:   ctx.NewID().String(),
		Role: "tool",
		Content: []domain.ContentBlock{{
			Type:      domain.ContentBlockToolResult,
			ToolUseID: planCall.ID,
			Text:      errMsg,
			IsError:   true,
		}},
	})
}

func (e *turnEngine) refreshChildLiveness(ctx actor.Context) {
	if e.onChildLiveness == nil {
		return
	}
	var dead []pendingChild
	for _, pc := range e.pendingChildren {
		activityAt, err := e.onChildLiveness(ctx, pc.ToolUseID)
		if err != nil {
			if e.logger != nil {
				e.logger.Warn("dispatch: child liveness query failed", "tool_use_id", pc.ToolUseID, "error", err)
			}
			fails := e.incrLivenessFailures(pc.StepID)
			if fails >= domain.ChildLivenessFailureThreshold {
				dead = append(dead, pc)
			}
			continue
		}
		e.childLivenessFailures.Delete(pc.StepID)
		e.childProgressAt.Store(pc.StepID, activityAt.UnixNano())
	}
	if len(dead) == 0 {
		return
	}
	deadSet := make(map[string]bool, len(dead))
	for _, pc := range dead {
		e.evictPendingChild(ctx, e.turnID, pc, fmt.Sprintf("child agent unreachable after %d consecutive liveness failures", domain.ChildLivenessFailureThreshold))
		deadSet[pc.ToolUseID] = true
	}
	var remaining []pendingChild
	for _, pc := range e.pendingChildren {
		if !deadSet[pc.ToolUseID] {
			remaining = append(remaining, pc)
		}
	}
	e.pendingChildren = remaining
}

// hasSyncPendingChildren reports whether any non-async fork child is still
// pending. Async children never hold the batch: phaseDispatch/phaseExecute
// proceed while they run and the parent harvests them via agent_wait.
func (e *turnEngine) hasSyncPendingChildren() bool {
	for _, pc := range e.pendingChildren {
		if !pc.Async {
			return true
		}
	}
	return false
}

// childLastActivity 返回指定子 agent 的最后活动时间。
// 优先使用 childProgressAt 中记录的最后 progress/heartbeat 时间，否则 fallback 到 SpawnedAt。
func (e *turnEngine) childLastActivity(pc pendingChild) time.Time {
	if v, ok := e.childProgressAt.Load(pc.StepID); ok {
		return time.Unix(0, v.(int64))
	}
	return pc.SpawnedAt
}

// earliestChildDeadline 返回所有 pending children 中最早的 idle deadline。
// 不设绝对寿命上限：合法 child 的单次 dispatch iteration 可能超过 15 分钟
// （PlanGoalStreamIdleTimeout），绝对寿命会误杀正常运行中的 child。idle
// timeout + child 独立 ticker 已足以检测真正的失联。
func (e *turnEngine) earliestChildDeadline() time.Time {
	var deadline time.Time
	for _, pc := range e.pendingChildren {
		d := e.childLastActivity(pc).Add(domain.ChildAgentIdleTimeout)
		if deadline.IsZero() || d.Before(deadline) {
			deadline = d
		}
	}
	return deadline
}

// handleChildTimeout 处理子 agent 失联超时：把它们从 pendingChildren 中移除，
// 注入错误结果，并发出 step.error 事件。只有 child 超过 ChildAgentIdleTimeout
// 未发送任何 heartbeat/progress 时才触发——child 的独立 ticker 在正常运行期间
// 持续刷新计时，因此超时意味着 child 已真正失联（崩溃/panic/网络全断）。
func (e *turnEngine) handleChildTimeout(ctx actor.Context, turnID string) {
	now := time.Now()
	var remaining []pendingChild
	for _, pc := range e.pendingChildren {
		if now.Sub(e.childLastActivity(pc)) >= domain.ChildAgentIdleTimeout {
			e.evictPendingChild(ctx, turnID, pc,
				fmt.Sprintf("child agent unresponsive after %v of inactivity", domain.ChildAgentIdleTimeout))
		} else {
			remaining = append(remaining, pc)
		}
	}
	e.pendingChildren = remaining
}

// evictPendingChild removes a single child from tracking, calls onChildTimeout
// to clean up the parent's activeChildren, injects a synthetic error result
// so the LLM sees the failure, and marks the corresponding step as failed.
// Shared by handleChildTimeout (idle expiry) and refreshChildLiveness (dead
// child detection).
func (e *turnEngine) evictPendingChild(ctx actor.Context, turnID string, pc pendingChild, errText string) {
	if e.onChildTimeout != nil {
		e.onChildTimeout(ctx, pc.ToolUseID)
	}
	e.childProgressAt.Delete(pc.StepID)
	e.childLivenessFailures.Delete(pc.StepID)
	e.pendingChildSteps.Delete(pc.StepID)
	if e.logger != nil {
		e.logger.Error("dispatch: child agent evicted",
			"tool_use_id", pc.ToolUseID, "step_id", pc.StepID, "reason", errText)
	}
	e.injectChildError(fmt.Errorf("%s", errText), pc.ToolUseID)
	if pc.StepID != "" {
		if step, ok := e.stepByID[pc.StepID]; ok {
			step.State = "failed"
			step.Error = errText
			e.stepByID[pc.StepID] = step
			e.emitStepEvent(domain.StepEvent{
				Kind:   "step.error",
				StepID: pc.StepID,
				TurnID: turnID,
				Error:  errText,
			})
		}
	}
	e.actionCount++
}

// incrLivenessFailures increments and returns the consecutive liveness-check
// failure count for a child step.
func (e *turnEngine) incrLivenessFailures(stepID string) int {
	v, _ := e.childLivenessFailures.LoadOrStore(stepID, 0)
	fails := v.(int) + 1
	e.childLivenessFailures.Store(stepID, fails)
	return fails
}

// appendSyntheticTaskCreation injects a tool_use + tool_result pair that
// records a task created automatically when a plan is approved. This makes the
// created tasks visible in the LLM conversation history so the model does not
// redundantly call create_task again.
func (e *turnEngine) appendSyntheticTaskCreation(ctx actor.Context, task gen.Task) {
	input := gen.AgentTaskCreateReq{
		Subject:    task.Subject,
		ActiveForm: task.ActiveForm,
	}
	inputJSON, _ := json.Marshal(input)

	toolUseID := ctx.NewID().String()
	e.appendMessage(domain.ChatMessage{
		ID:   ctx.NewID().String(),
		Role: domain.ChatRoleAssistant,
		Content: []domain.ContentBlock{{
			Type:      domain.ContentBlockToolUse,
			ToolUseID: toolUseID,
			ToolName:  "create_task",
			Input:     string(inputJSON),
		}},
	})

	resp := gen.AgentTaskCreateResp{
		Task: gen.Task{
			ID:         task.ID,
			Subject:    task.Subject,
			Status:     task.Status,
			ActiveForm: task.ActiveForm,
		},
	}
	respJSON, _ := json.Marshal(resp)
	e.appendMessage(domain.ChatMessage{
		ID:   ctx.NewID().String(),
		Role: domain.ChatRoleTool,
		Content: []domain.ContentBlock{{
			Type:      domain.ContentBlockToolResult,
			ToolUseID: toolUseID,
			Text:      string(respJSON),
		}},
	})
}

// handleChildResult 处理单个 fork 子 agent 的返回。
// 根据 toolUseID 找到对应的 pendingChild，移除它，然后注入结果或错误。
func (e *turnEngine) handleChildResult(ctx actor.Context, turnID string, result childResult) {
	var stepID, childKind string
	found := false
	for i, pc := range e.pendingChildren {
		if pc.ToolUseID == result.ToolUseID {
			stepID = pc.StepID
			childKind = pc.Kind
			e.pendingChildren = append(e.pendingChildren[:i], e.pendingChildren[i+1:]...)
			e.childProgressAt.Delete(pc.StepID)
			e.pendingChildSteps.Delete(pc.StepID)
			found = true
			break
		}
	}
	if !found {
		// Async 子 agent 终态：不重写历史占位符（spawn 应答已收尾 step），
		// 记入 asyncChildResults 等 agent_wait 收割。
		for i, pc := range e.asyncPending {
			if pc.ToolUseID != result.ToolUseID {
				continue
			}
			e.recordAsyncChildOutcome(turnID, pc, result)
			e.asyncPending = append(e.asyncPending[:i], e.asyncPending[i+1:]...)
			e.childProgressAt.Delete(pc.StepID)
			return
		}
		e.logger.Warn("turnEngine: child result for unknown tool use", "toolUseID", result.ToolUseID)
		return
	}

	if result.Err != nil {
		e.injectChildError(result.Err, result.ToolUseID)
	} else {
		e.injectChildResult(result.Result, result.ToolUseID)
		e.totalUsage = mergeUsage(e.totalUsage, &domain.UsageData{
			InputTokens:              int64(result.Result.InputTokens),
			OutputTokens:             int64(result.Result.OutputTokens),
			CacheCreationInputTokens: int64(result.Result.CacheCreationInputTokens),
			CacheReadInputTokens:     int64(result.Result.CacheReadInputTokens),
		})
		if stepID != "" {
			e.resolveForkStep(stepID, result.ToolUseID, childKind, result.Result)
		}
		// Propagate the child agent's file changes into the parent turn so
		// they appear in the parent's TurnTail. Merge by path (child wins for
		// overlapping files) and emit a step.file_changes event so the live
		// timeline updates incrementally.
		if len(result.Result.FileChanges) > 0 {
			e.appendFileChanges(result.Result.FileChanges)
			if stepID != "" {
				e.emitStepEvent(domain.StepEvent{
					Kind:        "step.file_changes",
					StepID:      stepID,
					TurnID:      turnID,
					FileChanges: result.Result.FileChanges,
				})
			}
		}
	}
	e.actionCount++
}

// recordAsyncChildOutcome stores a finished async child's terminal state for a
// later agent_wait harvest, merging its usage and file changes into the parent
// turn immediately (billing and diff visibility must not wait on the harvest).
func (e *turnEngine) recordAsyncChildOutcome(turnID string, pc pendingChild, result childResult) {
	outcome := asyncChildOutcome{AgentID: pc.AgentID, Status: "completed"}
	if result.Err != nil {
		outcome.Status = "failed"
		outcome.Err = result.Err.Error()
	} else {
		outcome.Result = result.Result
		e.totalUsage = mergeUsage(e.totalUsage, &domain.UsageData{
			InputTokens:              int64(result.Result.InputTokens),
			OutputTokens:             int64(result.Result.OutputTokens),
			CacheCreationInputTokens: int64(result.Result.CacheCreationInputTokens),
			CacheReadInputTokens:     int64(result.Result.CacheReadInputTokens),
		})
		if len(result.Result.FileChanges) > 0 {
			e.appendFileChanges(result.Result.FileChanges)
			if pc.StepID != "" {
				e.emitStepEvent(domain.StepEvent{
					Kind:        "step.file_changes",
					StepID:      pc.StepID,
					TurnID:      turnID,
					FileChanges: result.Result.FileChanges,
				})
			}
		}
	}
	if e.asyncChildResults == nil {
		e.asyncChildResults = make(map[string]asyncChildOutcome)
	}
	e.asyncChildResults[pc.ToolUseID] = outcome
	e.actionCount++
}

// injectChildResult 用子 agent 的实际发现替换历史中的 fork_child 占位结果。
// 通过 toolUseID 作为 key 定位到对应消息块。
func (e *turnEngine) injectChildResult(result domain.ForkResult, toolUseID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := len(e.history) - 1; i >= 0; i-- {
		msg := e.history[i]
		if msg.Role != "tool" {
			continue
		}
		for j := range msg.Content {
			if msg.Content[j].ToolUseID == toolUseID {
				msg.Content[j].Text = result.Summary
				e.history[i] = msg
				// 同步更新 delta 中对应的占位内容。
				for k := range e.delta {
					if e.delta[k].Role == "tool" {
						for l := range e.delta[k].Content {
							if e.delta[k].Content[l].ToolUseID == toolUseID {
								e.delta[k].Content[l].Text = result.Summary
							}
						}
					}
				}
				return
			}
		}
	}
}

// injectChildError 把 fork_child 占位结果替换为错误消息，
// 让 LLM 知道子 agent 失败并可以重试。
func (e *turnEngine) injectChildError(err error, toolUseID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	errText := fmt.Sprintf("forked agent failed: %v", err)
	for i := len(e.history) - 1; i >= 0; i-- {
		msg := e.history[i]
		if msg.Role != "tool" {
			continue
		}
		for j := range msg.Content {
			if msg.Content[j].ToolUseID == toolUseID {
				msg.Content[j].Text = errText
				msg.Content[j].IsError = true
				e.history[i] = msg
				// 同步更新 delta 中对应的占位内容。
				for k := range e.delta {
					if e.delta[k].Role == "tool" {
						for l := range e.delta[k].Content {
							if e.delta[k].Content[l].ToolUseID == toolUseID {
								e.delta[k].Content[l].Text = errText
								e.delta[k].Content[l].IsError = true
							}
						}
					}
				}
				return
			}
		}
	}
}

// resolveForkStep 用子 agent 的探索结果更新 fork_child step：
// 设置友好标题、把 summary 作为 output，并发出 block.appended + step.closed。
func (e *turnEngine) resolveForkStep(stepID string, toolUseID string, childKind string, result domain.ForkResult) {
	e.mu.Lock()
	step, ok := e.stepByID[stepID]
	if !ok {
		e.mu.Unlock()
		return
	}
	step.State = "completed"
	step.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	step.Title = forkStepTitle(childKind)
	if result.Summary != "" {
		step.Output = result.Summary
	}
	e.stepByID[stepID] = step
	e.mu.Unlock()

	// emitStepEvent 现在持有自己的锁。放在 e.mu 临界区之外避免锁排队。
	e.emitStepEvent(domain.StepEvent{
		Kind:   "block.appended",
		StepID: stepID,
		TurnID: e.turnID,
		Block: &domain.ContentBlock{
			Type:      domain.ContentBlockToolResult,
			ToolUseID: toolUseID,
			Text:      result.Summary,
		},
	})
	e.emitStepEvent(domain.StepEvent{
		Kind:   "step.closed",
		StepID: stepID,
		TurnID: e.turnID,
	})
}

// isForkCallable returns true for any callable that spawns a non-persistent
// child agent tracked via pendingChildren.
func isForkCallable(callableID string) bool {
	return callableID == "workspace.agent_spawn_by_type"
}

// forkStepTitle returns the friendly UI title for a completed fork step.
// It keys off the child agent kind so the title is independent of the
// LLM-facing tool name.
func forkStepTitle(childKind string) string {
	switch childKind {
	case "general":
		return "General Result"
	case domain.AgentKindReviewer:
		return "Review Result"
	case "explorer":
		return "Explore Result"
	default:
		return "Explore Result"
	}
}

// executeToolBatch 执行一个 batch 的工具调用。
// 只读 batch 并行执行；可变或单调用 batch 顺序执行。
//
// 执行流程：
//
//	┌─────────────────────────┐
//	│  injectShellDirs        │ 给 shell 调用注入项目根目录
//	│  resolveServiceRefs     │ 预解析服务引用
//	│  injectForkChildIDs     │ 给 fork_child 注入上下文
//	└───────────┬─────────────┘
//	            ↓
//	┌─────────────────────────┐
//	│  len==1 或 非只读 ?      │ 是 → 顺序 runOneCall
//	└───────────┬─────────────┘
//	            ↓ 否（多个只读）
//	┌─────────────────────────┐
//	│  goroutine 池并行执行     │
//	│  wg.Wait() 等待完成       │
//	└─────────────────────────┘
func (e *turnEngine) executeToolBatch(ctx actor.Context, batch toolExecutionBatch) []toolExecutionResult {
	results := make([]toolExecutionResult, len(batch.calls))
	if len(batch.calls) == 0 {
		return results
	}

	// Keep the parent's liveness timer alive while tools run. A silent, long
	// tool (e.g. `sleep`, a slow build) produces no flushEvents and therefore
	// no onFlushProgress → touchChildActivity, so without this heartbeat the
	// parent's ChildAgentIdleTimeout would misfire after 2 minutes of silence.
	if e.onToolHeartbeat != nil {
		if stop := e.onToolHeartbeat(); stop != nil {
			defer stop()
		}
	}

	// 引擎已经为这个 batch 做出权限决策（allow / bypass 放行 / 审批通过），因此也要
	// 在 actor 层为文件变更类调用补上 confirm=true。否则 yolo 等自动放行模式下，
	// 落在根目录外的写入会被 actor 当成越权直接报错（"outside all configured
	// roots"），而不是按已通过的权限决策执行。actor 层的根目录检查仍对绕过引擎的
	// 直接调用方生效。
	confirmFileMutatingCalls(batch)
	e.injectShellDirs(batch)
	e.injectProjectID(batch)
	e.injectCallerAgentID(batch)
	svcRefs := e.resolveServiceRefs(ctx, batch)
	planner := ctx.Planner()
	e.injectForkChildIDs(batch)

	// 只有整个 batch 全是只读调用时才并行执行；任何可变调用都顺序执行。
	allReadOnly := true
	for _, call := range batch.calls {
		if call.EffectKind != domain.EffectNone {
			allReadOnly = false
			break
		}
	}
	if len(batch.calls) == 1 || !allReadOnly {
		for i, call := range batch.calls {
			switch {
			case call.UnknownTool:
				results[i] = unknownToolResult(call)
			case call.MalformedArgs:
				results[i] = malformedArgsResult(call)
			default:
				results[i] = e.runOneCall(ctx, planner, call, svcRefs, e.batchSteps[i].ID)
			}
		}
		e.rewritePausedResults(results)
		return results
	}

	// 多个只读调用：并行执行以加速。
	var wg sync.WaitGroup
	wg.Add(len(batch.calls))
	for i, call := range batch.calls {
		go func(i int, call pendingToolCall) {
			defer wg.Done()
			switch {
			case call.UnknownTool:
				results[i] = unknownToolResult(call)
			case call.MalformedArgs:
				results[i] = malformedArgsResult(call)
			default:
				defer func() {
					if r := recover(); r != nil {
						results[i] = toolExecutionResult{call: call, out: fmt.Sprintf("tool call panicked: %v", r), isErr: true}
					}
				}()
				results[i] = e.runOneCall(ctx, planner, call, svcRefs, e.batchSteps[i].ID)
			}
		}(i, call)
	}
	wg.Wait()
	e.rewritePausedResults(results)
	return results
}

// rewritePausedResults 将因用户暂停而中断的工具结果改写为 "paused by user"，
// 使主循环的 pause 检查点能立即进入 LoopPaused，而不必等待工具自然完成。
// 仅当 pauseRequested 为 true 时才改写；非暂停的取消（stop/cancel）保持原消息。
func (e *turnEngine) rewritePausedResults(results []toolExecutionResult) {
	e.mu.Lock()
	paused := e.pauseRequested
	e.mu.Unlock()
	if !paused {
		return
	}
	for i := range results {
		if results[i].isErr && results[i].out == cancelledToolMsg {
			results[i].out = pausedToolMsg
		}
	}
}

// injectShellDirs 当 project.shell_exec/shell.bash/shell.exec 没有显式 Dir 时，
// 注入工作目录。优先使用 agent 绑定的 worktree 根（确保 bound worker 的
// shell 命令落在 worktree 内而非主仓库），否则回退到 CompiledContext.ProjectRoot。
func (e *turnEngine) injectShellDirs(batch toolExecutionBatch) {
	dir := ""
	if e.worktreeRoot != nil {
		dir = e.worktreeRoot()
	}
	if dir == "" {
		dir = e.startReq.CompiledContext.ProjectRoot
	}
	if dir == "" {
		return
	}
	for i := range batch.calls {
		switch batch.calls[i].CallableID {
		case "project.shell_exec", "shell.bash", "shell.exec":
			batch.calls[i].Input = injectDir(batch.calls[i].Input, dir)
		}
	}
}

// injectProjectID 当 workspace.git_* 调用没有显式 ProjectId 时，
// 注入 agent 所属项目的 actor ID。project.git_* 已通过父项目作用域解析，
// 不需要再注入 ProjectId。
func (e *turnEngine) injectProjectID(batch toolExecutionBatch) {
	pid := e.projectID
	if pid == "" {
		return
	}
	for i := range batch.calls {
		if batch.calls[i].ServiceName == "workspace" && strings.HasPrefix(batch.calls[i].CallableID, "workspace.git_") {
			batch.calls[i].Input = injectField(batch.calls[i].Input, "ProjectId", pid)
		}
	}
}

// injectCallerAgentID injects the calling agent's actor ID into workspace
// workflow callables. The workspace uses it for three purposes: active-workflow
// validation (spawn_assign/assign), direct-parent authorization
// (review/terminate), and marking the caller's own entry "(self)" in
// list_agents. The framework's planner.Call routes through the target's
// appRef.Invoke which sets sender=target (not the calling agent), so
// ctx.Caller() in the workspace handler returns the workspace actor, not the
// agent. This injection bridges that gap.
func (e *turnEngine) injectCallerAgentID(batch toolExecutionBatch) {
	agentID := e.agentID
	if agentID == "" {
		return
	}
	for i := range batch.calls {
		switch batch.calls[i].CallableID {
		case "workspace.agent_spawn_assign", "workspace.agent_assign",
			"workspace.agent_review", "workspace.agent_terminate",
			"workspace.agent_spawn_by_type",
			"workspace.agent_spawn_swarm",
			"workspace.agent_pause", "workspace.agent_resume",
			"workspace.agent_send_message", "workspace.agent_read_message",
			"workspace.list_agents",
			"project.review_changeset", "project.review_file_content",
			"appmanager.dev_generate", "appmanager.dev_gate",
			"appmanager.register_project", "appmanager.reload_project",
			"appmanager.sdk_vendor", "appmanager.app_export":
			// Unconditionally overwrite: an LLM or tool may pre-fill a forged
			// CallerAgentId. The injected value is the authoritative caller
			// identity from the turn engine, not client-supplied data.
			batch.calls[i].Input = overwriteField(batch.calls[i].Input, "CallerAgentId", agentID)
		case "appmanager.invoke", "appmanager.cast", "appmanager.emit",
			"appmanager.agent_action":
			// Same injection contract, different field name: appmanager
			// callables carry the caller agent identity as AgentId. Without
			// it an agent-originated bridge invoke is denied with
			// identity_incomplete (the planner routes through the target
			// actor, so ctx.Identity() is zero and the fallback never fires).
			batch.calls[i].Input = overwriteField(batch.calls[i].Input, "AgentId", agentID)
			// WorkspaceId is host-stamped the same way: plugin-issued llm.*
			// reverse calls need the workspace ACTOR id for aistats
			// attribution, and the field must be authoritative (never
			// LLM-supplied) like AgentId.
			if e.workspaceID != "" {
				batch.calls[i].Input = overwriteField(batch.calls[i].Input, "WorkspaceId", e.workspaceID)
			}
		}
	}
}

// resolveServiceRefs 预解析 batch 中所有调用需要的服务 actor 引用。
// Agent-local callables (no-dot IDs like plan_submit, memory_save) derive a
// ServiceName that no exposed service matches, so they fall back to ctx.Self().
// Remote callables (one-dot IDs like project.read) derive a ServiceName
// that matches an exposed service (e.g. "project") and route to that actor.
func (e *turnEngine) resolveServiceRefs(ctx actor.Context, batch toolExecutionBatch) map[string]ref.Ref {
	svcRefs := make(map[string]ref.Ref, 2)
	for _, call := range batch.calls {
		if _, ok := svcRefs[call.ServiceName]; ok {
			continue
		}
		if r, ok := ctx.LookupService(call.ServiceName); ok {
			svcRefs[call.ServiceName] = r
		} else {
			svcRefs[call.ServiceName] = ctx.Self()
		}
	}
	return svcRefs
}

// injectForkChildIDs 预先向 fork_child 请求中注入 stepID 和 toolUseID，
// 这样子 agent 完成后可以通过这些信息路由回父 turn。
// 同时提取 Async 标志：injectForkChildContext 的 JSON 往返会把它剥掉
// （workspace 请求结构体没有该字段），所以必须在注入前读到 pendingToolCall 上。
func (e *turnEngine) injectForkChildIDs(batch toolExecutionBatch) {
	goal := e.resolveGoalCondition()
	for i := range batch.calls {
		if i >= len(e.batchSteps) {
			break
		}
		if !isForkCallable(batch.calls[i].CallableID) {
			continue
		}
		kind := e.forkKindForLLMName(batch.calls[i].LLMName)
		batch.calls[i].ForkAsync = forkInputIsAsync(batch.calls[i].Input)
		batch.calls[i].Input = injectForkChildContext(
			batch.calls[i].Input,
			e.batchSteps[i].ID,
			e.turnID,
			batch.calls[i].ID,
			kind,
			goal,
		)
	}
}

// forkInputIsAsync reports whether a fork tool call set Async=true. Invalid
// JSON simply yields false — the spawn still proceeds synchronously, which is
// the safe default.
func forkInputIsAsync(input string) bool {
	var probe struct {
		Async bool `json:"Async"`
	}
	if err := json.Unmarshal([]byte(input), &probe); err != nil {
		return false
	}
	return probe.Async
}

// resolveGoalCondition returns the active session goal text, or "" when no
// callback is wired (e.g. in tests). Used to backfill a reviewer child's
// ReviewText at injection time.
func (e *turnEngine) resolveGoalCondition() string {
	if e.onGoalCondition == nil {
		return ""
	}
	return e.onGoalCondition()
}

// forkKindForLLMName maps the LLM-facing tool name to the child agent kind
// via the bundle-driven forkKindMap. The mapping lives in bundle card
// data.fork declarations on the agent's mounted bundle cards.
func (e *turnEngine) forkKindForLLMName(llmName string) string {
	if e.forkKindMap == nil {
		return ""
	}
	return e.forkKindMap[llmName]
}

func unknownToolResult(call pendingToolCall) toolExecutionResult {
	return toolExecutionResult{
		call:  call,
		out:   fmt.Sprintf("unknown tool: %q", call.LLMName),
		isErr: true,
	}
}

// malformedArgsResult 为参数 JSON 不可修复的 tool_call 生成不执行的错误结果。
// 模型会看到这次失败并重试；相比旧逻辑静默替换 {} 执行（可能误触发真实副作用
// 或返回误导性的 exit 0），显式失败保持了因果链完整。RawToolCall 在修复回退
// 之前生成，其长度可作为原始参数大小的参考。
func malformedArgsResult(call pendingToolCall) toolExecutionResult {
	return toolExecutionResult{
		call:  call,
		out:   fmt.Sprintf("tool call not executed: arguments JSON malformed beyond repair (raw call %d bytes), resend with valid JSON arguments", len(call.RawToolCall)),
		isErr: true,
	}
}

// runOneCall 执行单个工具调用，包括 shell 拦截和调用后钩子。
// 线程安全，可在 goroutine 中并发使用。
func (e *turnEngine) runOneCall(ctx actor.Context, planner actor.Planner, call pendingToolCall, svcRefs map[string]ref.Ref, stepID string) toolExecutionResult {
	// Normalize LLM-generated input keys to match the tool's declared schema.
	// This makes all agent toolcalls case-insensitive without touching gospore.
	if spec := e.toolSpecByCallableID(call.CallableID); spec != nil {
		if normalized, err := normalizeToolInputKeys(call.Input, spec.InputSchema); err == nil {
			call.Input = normalized
		}
	}

	if call.CallableID == "turn_assess" {
		var assessment gen.TurnAssessment
		if err := json.Unmarshal([]byte(call.Input), &assessment); err != nil {
			return toolExecutionResult{call: call, out: "invalid turn.assess input: " + err.Error(), isErr: true}
		}
		assessment.Decision = strings.TrimSpace(assessment.Decision)
		if assessment.Decision != "complete_candidate" && assessment.Decision != "ready_for_review" {
			return toolExecutionResult{call: call, out: "turn.assess Decision must be \"complete_candidate\" or \"ready_for_review\" — do not call turn.assess while the goal still needs work; the system continues automatically", isErr: true}
		}
		if e.assessValidator != nil {
			if err := e.assessValidator(assessment.Decision); err != nil {
				return toolExecutionResult{call: call, out: err.Error(), isErr: true}
			}
		}
		e.mu.Lock()
		e.assessment = cloneTurnAssessment(&assessment)
		e.mu.Unlock()
		return toolExecutionResult{call: call, out: "Turn assessment recorded.", isErr: false}
	}
	svcRef, ok := svcRefs[call.ServiceName]
	if !ok {
		return toolExecutionResult{call: call, out: fmt.Sprintf("service %q not available", call.ServiceName), isErr: true}
	}
	toolLifecycle, toolLifecycleCancel := e.lifecycleWithCancel(ctx.Lifecycle())
	defer toolLifecycleCancel()
	ctx = turnActorContext{Context: ctx, lifecycle: toolLifecycle}

	// MCP tool routing: the synthetic callable "mcp.<serverID>.<toolName>"
	// (injected by resolveMCPTools) is executed by mcpmanager.call_tool; the
	// result text is backfilled into the tool frame by the caller.
	if serverID, toolName, ok := parseMCPToolCallable(call.CallableID); ok {
		return e.runMCPToolCall(ctx, planner, svcRefs, call, serverID, toolName)
	}

	// App tool routing: the synthetic callable "app.<appID>.<callable>"
	// (injected by resolveAppTools) is executed by appmanager.invoke; the
	// response payload is decoded and returned as tool frame text. Because app
	// IDs may contain dots, the registered app ID set (fetched once per turn
	// from appmanager.list) disambiguates the split.
	if strings.HasPrefix(call.CallableID, "app.") {
		if appID, callableName, ok := parseAppToolCallable(call.CallableID, e.knownAppIDs(ctx, planner, svcRefs)); ok {
			return e.runAppToolCall(ctx, planner, svcRefs, call, appID, callableName)
		}
	}

	// 先尝试 shell.exec / project.shell_exec 路由：VFS / project file / bash / reject。
	if out, isErr, intercepted := tryShellExecIntercept(ctx, planner, call, svcRefs); intercepted {
		return toolExecutionResult{call: call, out: out, isErr: isErr}
	}
	// 再尝试 shell.bash 拦截（向后兼容）。
	if out, isErr, intercepted := tryShellIntercept(ctx, planner, call, svcRefs); intercepted {
		return toolExecutionResult{call: call, out: out, isErr: isErr}
	}
	// 流式 shell callable：通过 planner.Stream 消费 stdout/stderr/exit chunk，
	// 每个 stdout/stderr chunk 都 buffer 成 turn.execution_output_delta 事件。
	if spec := e.toolSpecByCallableID(call.CallableID); spec != nil && spec.Stream {
		out, isErr := e.callStreamingTool(ctx, planner, svcRef, call, stepID)
		if !isErr && e.onToolExecuted != nil {
			e.onToolExecuted(ctx, call.CallableID, call.Input)
		}
		return toolExecutionResult{call: call, out: out, isErr: isErr}
	}

	// 对可能修改或删除文件的工具，在执行前预读旧内容，以便生成准确 diff。
	preReadContent, preReadOk := e.maybePreReadForFileChange(ctx, planner, svcRef, call)

	// appmanager.invoke 的 Payload 是 bytes 字段；LLM 工具入参给的是 JSON
	// object/array。在透传给 gospore handler 前把 Payload 序列化为 bytes，
	// 否则 handler 在 json decode AppManagerInvokeReq 时对 []byte 字段解
	// object 会失败（"cannot unmarshal object into ... of type []uint8"）。
	input := call.Input
	if call.CallableID == "appmanager.invoke" {
		encoded, err := encodeAppManagerInvokeInput(input)
		if err != nil {
			return toolExecutionResult{call: call, out: fmt.Sprintf("appmanager.invoke: encode payload: %v", err), isErr: true}
		}
		input = encoded
	}

	out, isErr, rawResult := callTool(toolLifecycle, planner, svcRef, call.CallableID, input)
	if !isErr && e.onToolExecuted != nil {
		e.onToolExecuted(ctx, call.CallableID, call.Input)
	}
	var fcs []domain.TurnFileChange
	if !isErr {
		fcs = e.fileChangesFromResult(call, rawResult, preReadContent, preReadOk)
	}
	result := toolExecutionResult{call: call, out: out, isErr: isErr, fileChanges: fcs}
	// Screenshot responses never inline their base64 into the tool_result:
	// the LLM gets compact metadata plus the image as a follow-up user-role
	// observation message (same embedding as user-submitted chat images).
	if !isErr {
		if resp, ok := rawResult.(domain.ComputerUseScreenshotResp); ok {
			if metaText, obs := screenshotObservation(resp); obs != nil {
				result.out = metaText
				result.observation = obs
				result.observationMeta = "screenshot"
			}
		}
	}
	return result
}

// maybePreReadForFileChange 对 file_write / file_rm 在执行前预读旧内容，
// 用于后续生成准确的添加/删除 diff。非文件工具返回空。
func (e *turnEngine) maybePreReadForFileChange(
	ctx actor.Context,
	planner actor.Planner,
	svcRef ref.Ref,
	call pendingToolCall,
) (string, bool) {
	switch call.CallableID {
	case "project.write", "project.rm":
		path := filePathFromInput(call.Input)
		if path == "" {
			return "", false
		}
		return e.readFileContent(ctx, planner, svcRef, path)
	}
	return "", false
}

// 流式 shell 输出的合并 flush 阈值：缓冲达到 shellStreamFlushMaxBytes，
// 或距上次 flush 达到 shellStreamFlushInterval，即把缓冲的 stdout/stderr
// 文本合并 emit 为 step.execution_progress 并 flushEvents。exit chunk 与
// 流结束时强制 flush 剩余缓冲。
const (
	shellStreamFlushMaxBytes = 4 * 1024
	shellStreamFlushInterval = 50 * time.Millisecond
)

// shellStreamFlusher 把流式 shell call 的 stdout/stderr chunk 合并后再
// emitStepEvent + flushEvents。逐 chunk 立即 flush 会让快速输出（如 go build）
// 每秒触发几十次 flushEvents，每次走 onStepEvent + EmitEvent + 防抖 snapshot，
// 全部压在 agent_exec lane 上。合并后单条事件携带更多文本，flush 次数由
// 输出速率决定的上限变为由阈值决定。
//
// 事件顺序：segs 按到达顺序保存，flush 时相邻同 stream 段合并为一条
// {stream,text} 事件，跨 stream 的先后关系保持不变。exit chunk 处理与
// 流结束后的 tool_result 事件都在强制 flush 之后，EventSeq 单调性不受影响。
//
// 并发：chunk 回调跑在 planner.Stream 的 promise goroutine 上，定时 flush
// 跑在独立 timer goroutine 上，两者通过 mu 串行化；持锁覆盖 emit+flush
// 全过程，保证两条 flush 路径不会交错发出半合并的事件。
type shellStreamFlusher struct {
	ctx    actor.Context
	engine *turnEngine
	stepID string
	turnID string

	mu        sync.Mutex
	segs      []shellStreamSeg
	bufBytes  int
	lastFlush time.Time
}

type shellStreamSeg struct {
	stream string
	text   string
}

func newShellStreamFlusher(ctx actor.Context, e *turnEngine, stepID, turnID string) *shellStreamFlusher {
	return &shellStreamFlusher{
		ctx:       ctx,
		engine:    e,
		stepID:    stepID,
		turnID:    turnID,
		lastFlush: time.Now(),
	}
}

// add 缓冲一个 stdout/stderr chunk，并在大小或时间阈值满足时 flush。
// 空文本直接丢弃（与逐 chunk 时代的行为一致）。
func (f *shellStreamFlusher) add(stream, text string) {
	if text == "" {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if n := len(f.segs); n > 0 && f.segs[n-1].stream == stream {
		f.segs[n-1].text += text
	} else {
		f.segs = append(f.segs, shellStreamSeg{stream: stream, text: text})
	}
	f.bufBytes += len(text)
	if f.bufBytes >= shellStreamFlushMaxBytes || time.Since(f.lastFlush) >= shellStreamFlushInterval {
		f.flushLocked()
	}
}

// flushIfDue 供 timer goroutine 调用：仅当距上次 flush 超过间隔且缓冲非空
// 时才 flush，避免无输出时空转 flushEvents。
func (f *shellStreamFlusher) flushIfDue() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.bufBytes == 0 || time.Since(f.lastFlush) < shellStreamFlushInterval {
		return
	}
	f.flushLocked()
}

// flush 强制 flush 剩余缓冲（exit chunk / 流结束 / defer 兜底）。
func (f *shellStreamFlusher) flush() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.flushLocked()
}

// flushLocked 发出合并事件并 flushEvents。调用方必须持有 f.mu。
// 单个段的 marshal 失败不中断其余段（与逐 chunk 时代的宽容行为一致）；
// flushEvents 错误只 Warn 不中断流。
func (f *shellStreamFlusher) flushLocked() {
	if f.bufBytes == 0 {
		return
	}
	segs := f.segs
	f.segs = nil
	f.bufBytes = 0
	f.lastFlush = time.Now()
	for _, seg := range segs {
		progress, err := json.Marshal(map[string]string{
			"stream": seg.stream,
			"text":   seg.text,
		})
		if err != nil {
			continue
		}
		f.engine.emitStepEvent(domain.StepEvent{
			Kind:     "step.execution_progress",
			StepID:   f.stepID,
			TurnID:   f.turnID,
			Progress: string(progress),
		})
	}
	// Flush the step-event buffer immediately so the UI sees streaming
	// output while the process is still running. The shell callback is
	// synchronous, so waiting until phaseCommit would batch all stdout
	// until the command exits.
	if err := f.engine.flushEvents(f.ctx); err != nil {
		f.engine.logger.Warn("turnEngine: streaming flushEvents failed", "err", err)
	}
}

// callStreamingTool dispatches a streaming shell callable and translates
// chunks into turn events. The exit chunk is assembled into the same JSON
// shape (ShellBashResp / ShellExecResp) that the unary path produced.
// Non-zero exit codes are treated as normal command output (stderr is shown
// as RunCommand in the UI), not as a flow error.
func (e *turnEngine) callStreamingTool(
	ctx actor.Context,
	planner actor.Planner,
	svcRef ref.Ref,
	call pendingToolCall,
	stepID string,
) (string, bool) {
	lifecycleCtx, lifecycleCancel := e.lifecycleWithCancel(ctx.Lifecycle())
	defer lifecycleCancel()
	budget := shellStreamBudget(shellReqTimeoutMs(call.Input))
	callCtx, cancel := context.WithTimeout(lifecycleCtx, budget)
	defer cancel()

	turnID := e.turnID
	var finalResult string
	var finalIsErr bool
	gotExit := false

	flusher := newShellStreamFlusher(ctx, e, stepID, turnID)
	// 定时 flush：chunk 回调只在 chunk 到达时触发，输出暂停期间缓冲的尾部
	// 文本靠 ticker 在 50ms 阈值后推出去，避免 UI 长时间看不到尾部输出。
	// 与 onChunk 一样，本 goroutine 调用 emitStepEvent/flushEvents 属于既有
	// 做法（planner.Stream 的消费循环本就跑在 promise goroutine 上），
	// flusher.mu 保证两条 flush 路径串行。
	stopFlushTimer := make(chan struct{})
	defer close(stopFlushTimer)
	go func() {
		ticker := time.NewTicker(shellStreamFlushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stopFlushTimer:
				return
			case <-ticker.C:
				flusher.flushIfDue()
			}
		}
	}()
	// 兜底：exit、错误返回、cancel 等所有出口都先把剩余缓冲冲出去，
	// 保证 execution_progress 事件的 EventSeq 先于后续 tool_result。
	defer flusher.flush()

	onChunk := func(chunkAny any) error {
		if call.CallableID == "computeruse.stream" {
			chunk, ok := chunkAny.(domain.ComputerUseStreamChunk)
			if !ok {
				return nil
			}
			b, err := json.Marshal(chunk)
			if err != nil {
				return err
			}
			finalResult = string(b)
			finalIsErr = false
			gotExit = true
			return io.EOF
		}

		chunk, ok := chunkAny.(domain.ShellChunk)
		if !ok {
			return nil
		}
		switch chunk.Kind {
		case "stdout", "stderr":
			// Buffer and coalesce with adjacent same-stream chunks; the
			// flusher emits step.execution_progress + flushEvents only on
			// size/time thresholds, exit chunk, or stream end.
			flusher.add(chunk.Kind, chunk.Text)
		case "exit":
			// 先把缓冲的输出冲出去，再捕获退出结果，保证进度事件
			// 的 EventSeq 先于后续 tool_result 事件。
			flusher.flush()
			// Marshal exit chunk into the same JSON shape as the unary path.
			// Callers see ShellBashResp for shell.bash and ShellExecResp for
			// shell.exec / project.shell_exec; both share Stdout/Stderr/ExitCode
			// so the marshaled JSON is interchangeable for LLM tool_result purposes.
			resp := domain.ShellBashResp{
				Stdout:                   chunk.Stdout,
				Stderr:                   chunk.Stderr,
				ExitCode:                 chunk.ExitCode,
				Truncated:                chunk.Truncated,
				Interrupted:              chunk.Interrupted,
				ReturnCodeInterpretation: chunk.ReturnCodeInterpretation,
			}
			if chunk.DirWarning != "" && !strings.Contains(resp.Stderr, chunk.DirWarning) {
				resp.Stderr = chunk.DirWarning + "\n" + resp.Stderr
			}
			b, _ := json.Marshal(resp)
			finalResult = string(b)
			finalIsErr = false
			gotExit = true
			return io.EOF // authoritative result captured; stop consuming
		}
		return nil
	}

	p := planner.Stream(callCtx, svcRef, call.CallableID, []byte(call.Input), onChunk)
	_, err := p.Await()
	if err != nil && !gotExit {
		// planner.Stream surfaces consumer-ctx cancellation as the ctx's
		// own error (context.Canceled, translated since gospore 9f6ec27);
		// the bare invoke.ErrCallCancelled sentinel survives only for an
		// explicit Cancel. Classify by the context states, not the error
		// identity, so every cancellation shape routes to the paused-tool
		// rewrite instead of leaking a raw error to the LLM.
		msg, cancelled := shellNoExitCause(lifecycleCtx.Err(), callCtx.Err(), budget)
		if cancelled {
			return msg, true
		}
		if errors.Is(callCtx.Err(), context.DeadlineExceeded) {
			return "shell: " + msg, true
		}
		return fmt.Sprintf("shell: %v", err), true
	}
	if !gotExit {
		// planner.Stream resolves cleanly (nil error) when its consumer
		// context is cancelled or times out: the pending slot is
		// unregistered, Recv returns io.EOF, and cancellation never
		// surfaces as a stream error. Recover the real cause from the
		// context states instead of reporting an opaque protocol symptom.
		msg, cancelled := shellNoExitCause(lifecycleCtx.Err(), callCtx.Err(), budget)
		if cancelled {
			return msg, true
		}
		return "shell: " + msg, true
	}
	return finalResult, finalIsErr
}

// shellStreamBudgetMargin covers process teardown after the shell actor kills
// the command at its own deadline: pipe drain, output truncation, and
// exit-chunk delivery.
const shellStreamBudgetMargin = time.Minute

// shellStreamBudgetMax caps a single streaming shell call even when the
// request asks for more.
const shellStreamBudgetMax = 30 * time.Minute

// shellStreamBudget derives the outer context budget for a streaming shell
// call. domain.ToolCallTimeout is the default; when the request carries an
// explicit timeout exceeding it, the budget grows to cover it plus margin,
// so a long command is not cut off by the caller before the shell actor's
// own timeout produces the exit chunk.
func shellStreamBudget(reqMs int32) time.Duration {
	requested := time.Duration(reqMs) * time.Millisecond
	if reqMs <= 0 || requested+shellStreamBudgetMargin <= domain.ToolCallTimeout {
		return domain.ToolCallTimeout
	}
	budget := requested + shellStreamBudgetMargin
	if budget > shellStreamBudgetMax {
		return shellStreamBudgetMax
	}
	return budget
}

// shellReqTimeoutMs extracts the Timeout field (milliseconds) from a shell
// tool-call JSON input. Returns 0 when absent or unparseable. json.Unmarshal
// matches field names case-insensitively, so both "timeout" and "Timeout"
// keys resolve.
func shellReqTimeoutMs(input string) int32 {
	var req struct {
		Timeout int32
	}
	if err := json.Unmarshal([]byte(input), &req); err != nil {
		return 0
	}
	return req.Timeout
}

// shellNoExitCause explains a shell stream that ended without an exit chunk.
// planner.Stream resolves its promise cleanly when the consumer context is
// cancelled or times out, so the cause must be recovered from the context
// states, not the stream error.
func shellNoExitCause(lifecycleErr, callErr error, budget time.Duration) (msg string, cancelled bool) {
	switch {
	case lifecycleErr != nil:
		return cancelledToolMsg, true
	case errors.Is(callErr, context.DeadlineExceeded):
		return fmt.Sprintf("timed out after %s waiting for the command to exit; pass a larger timeout or split the command", budget), false
	default:
		return "stream ended without exit chunk", false
	}
}

// processBatchResults 处理执行后事务：发送事件、更新 step、
// 把 tool_result 消息追加到历史，并累计本轮文件变更。
func (e *turnEngine) processBatchResults(ctx actor.Context, turnID string, results []toolExecutionResult, batchSteps []domain.TurnAction) error {
	for i, result := range results {
		if err := e.applySingleToolResult(ctx, turnID, result, batchSteps[i]); err != nil {
			return err
		}
		// Defense-in-depth: runOneCall already clears fileChanges on isErr, but
		// reject any future regression that bypasses the upstream guard so ghost
		// file changes can never reach the live timeline on a failed tool call.
		if !result.isErr && len(result.fileChanges) > 0 {
			e.appendFileChanges(result.fileChanges)
			// 把每个工具结果的 fileChanges 单独 emit 成 step 事件，前端按
			// (turnId, path) 增量累积。step.file_changes 与 step.task_* 同构，
			// 共享 EventSeq 去重和 flushEvents 原子快照。
			e.emitStepEvent(domain.StepEvent{
				Kind:        "step.file_changes",
				StepID:      batchSteps[i].ID,
				TurnID:      turnID,
				FileChanges: result.fileChanges,
			})
		}
	}
	return nil
}

// applySingleToolResult 应用单个工具结果：
// 1. 更新 step Output/Text 并 emit block.appended
// 2. 根据错误/fork_child/正常三种情况设置 step 状态
// 3. emit step.error 或 step.closed
// 4. 把 tool_result 消息 append 到 history
func (e *turnEngine) applySingleToolResult(ctx actor.Context, turnID string, result toolExecutionResult, toolCallStep domain.TurnAction) error {
	// 这个 tool_use 已经关闭，无论成功/失败/拒绝都要减计数。
	if e.openToolCalls > 0 {
		defer func() { e.openToolCalls-- }()
	}

	// Clean up framework/severity prefixes from error messages before they reach
	// the UI, diagnostics, or the LLM history.
	msg := result.out
	severity := "error"
	if result.isErr {
		msg, severity = normalizeToolError(result.out)
	}

	// Snapshot large read outputs for UI to reduce turn history size.
	// The LLM history still receives the full content; only the frontend
	// step/block payload is replaced with a reference.
	displayMsg := msg
	if !result.isErr && e.saveSnapshot != nil && result.call.CallableID == "project.read" && len(msg) > 4096 {
		var input struct {
			Path   string `json:"path"`
			Offset int    `json:"offset,omitempty"`
			Limit  int    `json:"limit,omitempty"`
		}
		if err := json.Unmarshal([]byte(result.call.Input), &input); err == nil {
			// Parse the actual read span from the response so the UI can show an
			// accurate line range. The raw input offset/limit are 0 when the agent
			// omitted them, which previously produced a bogus "0–-1" range.
			var resp struct {
				StartLine  int32 `json:"StartLine"`
				NumLines   int32 `json:"NumLines"`
				TotalLines int32 `json:"TotalLines"`
			}
			_ = json.Unmarshal([]byte(msg), &resp)

			snapshotID := ctx.NewID().String()
			if err := e.saveSnapshot(snapshotID, []byte(msg)); err == nil {
				refFields := map[string]any{
					"id":         snapshotID,
					"path":       input.Path,
					"size":       len(msg),
					"startLine":  resp.StartLine,
					"numLines":   resp.NumLines,
					"totalLines": resp.TotalLines,
				}
				if input.Offset > 0 {
					refFields["offset"] = input.Offset
				}
				if input.Limit > 0 {
					refFields["limit"] = input.Limit
				}
				ref := map[string]any{"__snapshotRef": refFields}
				if refBytes, err := json.Marshal(ref); err == nil {
					displayMsg = string(refBytes)
				}
			}
		}
	}

	toolCallStep.Output = displayMsg
	toolCallStep.Text = displayMsg
	e.upsertStep(toolCallStep)
	e.actionCount++
	resultBlockType := domain.ContentBlockToolResult
	if !result.isErr && result.call.CallableID == "memory_save" {
		resultBlockType = "memory_result"
	}
	e.emitStepEvent(domain.StepEvent{
		Kind:   "block.appended",
		StepID: toolCallStep.ID,
		TurnID: turnID,
		Block: &domain.ContentBlock{
			Type:      resultBlockType,
			ToolUseID: result.call.ID,
			Text:      displayMsg,
			IsError:   result.isErr,
		},
	})

	isForkChild := isForkCallable(toolCallStep.CallableID)
	// Async fork 的 step 立即以 spawn 应答收尾；只有同步 fork 保持
	// running 等 resolveForkStep 关闭。
	isSyncForkChild := isForkChild && !result.call.ForkAsync
	switch {
	case result.isErr:
		toolCallStep = failStep(toolCallStep, msg)
	case isSyncForkChild:
		// 保持 running，resolveForkStep 会在子 agent 完成时关闭它。
	default:
		toolCallStep.State = "completed"
	}
	e.upsertStep(toolCallStep)

	switch {
	case result.isErr:
		if e.onReportDiagnostic != nil {
			e.onReportDiagnostic(ctx, domain.OracleReportDiagnosticReq{
				Severity:      severity,
				Source:        "tool_call",
				Message:       msg,
				TurnID:        turnID,
				StepID:        toolCallStep.ID,
				CallableID:    result.call.CallableID,
				TargetService: result.call.ServiceName,
				ToolUseID:     result.call.ID,
				Input:         truncateForDiagnostic(result.call.Input),
				Output:        truncateForDiagnostic(result.out),
				Unit:          modelUnitPtrToOracle(e.startReq.Input.Unit),
				RawData:       result.call.RawToolCall,
			})
		}
		e.emitStepEvent(domain.StepEvent{
			Kind:   "step.error",
			StepID: toolCallStep.ID,
			TurnID: turnID,
			Error:  msg,
		})
	case !isSyncForkChild:
		e.emitStepEvent(domain.StepEvent{
			Kind:   "step.closed",
			StepID: toolCallStep.ID,
			TurnID: turnID,
		})
	}

	e.logger.Debug("turnEngine: applySingleToolResult appending tool_result", "turnID", turnID, "toolUseId", result.call.ID, "callable", result.call.CallableID, "textLen", len(msg), "isErr", result.isErr)
	e.appendMessage(domain.ChatMessage{
		ID:      ctx.NewID().String(),
		Role:    "tool",
		Content: []domain.ContentBlock{{Type: domain.ContentBlockToolResult, ToolUseID: result.call.ID, Text: msg, IsError: result.isErr}},
	})
	if result.observation != nil {
		if result.observation.ID == "" {
			result.observation.ID = ctx.NewID().String()
		}
		obsMeta := result.observationMeta
		if obsMeta == "" {
			obsMeta = "screenshot"
		}
		e.appendMessage(*result.observation)
		// The observation is also a persisted user_inject step: compileMessages
		// maps user_inject back to a user-role history message (the processing
		// flow), while the UI renders it inside the ASSISTANT turn envelope —
		// the screenshot shows as an ai-step image, not a user bubble. The
		// recognition write-back (onImageRecognized) targets this step ID and
		// block index; recognition text itself stays wire-only.
		if len(result.observation.Content) > 0 {
			first := result.observation.Content[0]
			e.emitStepEvent(domain.StepEvent{
				Kind:     "step.opened",
				StepID:   result.observation.ID,
				TurnID:   turnID,
				StepType: "user_inject",
				Role:     "assistant",
				Block:    &first,
				Meta:     obsMeta,
			})
			for i := 1; i < len(result.observation.Content); i++ {
				block := result.observation.Content[i]
				e.emitStepEvent(domain.StepEvent{
					Kind:   "block.appended",
					StepID: result.observation.ID,
					TurnID: turnID,
					Block:  &block,
					Meta:   obsMeta,
				})
			}
			e.emitStepEvent(domain.StepEvent{
				Kind:   "step.closed",
				StepID: result.observation.ID,
				TurnID: turnID,
				Meta:   obsMeta,
			})
		}
	}
	return nil
}

// callTool 通过 planner.Call 分发单个工具调用。
// 返回 marshaled 字符串、是否出错，以及原始 typed result（供调用者提取结构化元数据）。
func callTool(lifecycleCtx context.Context, planner actor.Planner, svcRef ref.Ref, callableID, inputJSON string) (string, bool, any) {
	if !json.Valid([]byte(inputJSON)) {
		return fmt.Sprintf("malformed JSON input: %s", truncate(inputJSON, 200)), true, nil
	}
	payload := json.RawMessage(inputJSON)
	if len(payload) == 0 {
		payload = json.RawMessage("{}")
	}
	if planner == nil {
		return "planner not available", true, nil
	}

	callCtx, cancel := context.WithTimeout(lifecycleCtx, toolCallTimeoutFor(callableID))
	defer cancel()

	result, err := planner.Call(callCtx, svcRef, callableID, []byte(payload)).Await()
	if err != nil {
		if errors.Is(err, context.Canceled) && lifecycleCtx.Err() != nil {
			return cancelledToolMsg, true, nil
		}
		return err.Error(), true, nil
	}
	return marshalResult(result), false, result
}

// toolCallTimeoutFor returns the outer per-tool-call budget. Media generation
// tools (image_generate / video_generate) reserve the full media generation
// budget so a long-running 4K generation or video render is not cut by the
// generic 5-minute tool timeout. Plugin callables routed through
// appmanager.invoke may declare up to nativeToolTimeout. Every other tool uses
// domain.ToolCallTimeout.
func toolCallTimeoutFor(callableID string) time.Duration {
	switch callableID {
	case "image_generate", "video_generate":
		return mediaGenerationTimeout
	case "appmanager.invoke":
		return nativeToolTimeout
	default:
		return domain.ToolCallTimeout
	}
}

// marshalResult 把 callable 的返回值转换为字符串。
func marshalResult(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	// agent.skill.mount 返回 body 文本；unwrap 让 tool_result 直接是 raw body
	// 而不是 JSON 包装，与 slash 路径合成的 tool_result 字节级一致。
	if r, ok := v.(domain.AgentSkillMountResp); ok {
		if r.Body != "" {
			return r.Body
		}
	}
	if c := converter.LookupValue(v); c != nil {
		s, err := c(v)
		if err == nil {
			return s
		}
	}
	out, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("marshal: %v", err)
	}
	return string(out)
}

// overwriteField unconditionally sets a string field in the request JSON to the
// given value, replacing any pre-existing content. Unlike injectField (which
// only fills when empty), this prevents an LLM/tool from forging a privileged
// field such as CallerAgentId.
func overwriteField(inputJSON, field, value string) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(inputJSON), &m); err != nil {
		return inputJSON
	}
	m[field] = json.RawMessage(`"` + value + `"`)
	b, err := json.Marshal(m)
	if err != nil {
		return inputJSON
	}
	return string(b)
}

// injectField 在请求 JSON 的指定字段为空时注入给定值，已有非空值时不覆盖。
func injectField(inputJSON, field, value string) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(inputJSON), &m); err != nil {
		return inputJSON
	}
	if v, ok := m[field]; ok && string(v) != `""` && string(v) != "null" {
		return inputJSON
	}
	m[field] = json.RawMessage(`"` + value + `"`)
	b, err := json.Marshal(m)
	if err != nil {
		return inputJSON
	}
	return string(b)
}

// injectDir 把 shell 请求 JSON 的 Dir 解析成绝对路径，基准是 projectRoot：
//   - 缺字段或空串：注入 projectRoot
//   - 相对路径：filepath.Join(projectRoot, dir) 拼成绝对
//   - 绝对路径：原样保留
//
// 必须在这一层解析，否则 shell.bash 会用 filepath.Abs 按"进程 cwd"解析相对路径
// （桌面进程的 cwd 是 cmd/sporemind-desktop，不是项目根）。
func injectDir(inputJSON string, projectRoot string) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(inputJSON), &m); err != nil {
		return inputJSON
	}
	raw, ok := m["Dir"]
	var dir string
	if ok {
		if err := json.Unmarshal(raw, &dir); err != nil {
			return inputJSON
		}
	}
	if dir == "" {
		dir = projectRoot
	} else if filepath.IsAbs(dir) {
		return inputJSON
	} else {
		dir = filepath.Join(projectRoot, dir)
	}
	encoded, err := json.Marshal(dir)
	if err != nil {
		return inputJSON
	}
	m["Dir"] = json.RawMessage(encoded)
	b, err := json.Marshal(m)
	if err != nil {
		return inputJSON
	}
	return string(b)
}

// injectForkChildContext injects AgentKind, ParentStepId, ParentTurnId, ToolUseId
// into the workspace.agent_spawn_by_type tool input. Kind is derived from the LLM
// tool name by forkKindForLLMName and tells workspace.agent_spawn_by_type which
// child role to spawn. ParentTurnId is the active turn ID so the child can route
// explore_complete back to the originating parent turn. For reviewer children, a
// ReviewText omitted by the LLM is backfilled from goalCondition (the active
// session goal) and carried in the Prompt field (the workspace schema has no
// dedicated ReviewText field; the handler maps it back). If JSON parsing
// fails, falls back to best-effort string injection.
func injectForkChildContext(inputJSON, stepID, parentTurnID, toolUseID, kind, goalCondition string) string {
	var req domain.WorkspaceAgentSpawnByTypeReq
	if err := json.Unmarshal([]byte(inputJSON), &req); err != nil {
		slog.Warn("injectForkChildContext: malformed JSON from LLM, attempting manual injection",
			"error", err, "input", truncate(inputJSON, 200))
		// Best-effort: inject fields via string manipulation.
		injected := fmt.Sprintf(`{"AgentKind":%q,"ParentStepId":%q,"ParentTurnId":%q,"ToolUseId":%q,`, kind, stepID, parentTurnID, toolUseID)
		if idx := strings.Index(inputJSON, "{"); idx >= 0 {
			return inputJSON[:idx+1] + injected + inputJSON[idx+1:]
		}
		return fmt.Sprintf(`{"AgentKind":%q,"ParentStepId":%q,"ParentTurnId":%q,"ToolUseId":%q,"Description":"spawn_by_type","Prompt":""}`, kind, stepID, parentTurnID, toolUseID)
	}
	req.AgentKind = kind
	req.ParentStepID = stepID
	req.ParentTurnID = parentTurnID
	req.ToolUseID = toolUseID
	// For reviewer children, the review text is carried in the Prompt field
	// of WorkspaceAgentSpawnByTypeReq. Extract it from the LLM's ReviewText
	// field (if present) or backfill from the active session goal.
	if kind == domain.AgentKindReviewer {
		reviewText := ""
		var raw map[string]any
		if json.Unmarshal([]byte(inputJSON), &raw) == nil {
			if rt, ok := raw["ReviewText"].(string); ok {
				reviewText = rt
			}
			req.PlanEvidence = stringSlice(raw["PlanEvidence"])
		}
		if reviewText == "" {
			reviewText = goalCondition
		}
		req.Prompt = reviewText
	} else {
		// PlanEvidence is reviewer-only; drop it if a non-reviewer tool call
		// hallucinated the field past schema validation.
		req.PlanEvidence = nil
	}
	// fork_review's LLM schema carries ReviewText/PlanEvidence only — no
	// Description — but workspace.agent_spawn_by_type rejects an empty
	// Description. Backfill it from the prompt for context, else the kind.
	if strings.TrimSpace(req.Description) == "" {
		req.Description = forkDescriptionFallback(kind, req.Prompt)
	}
	b, err := json.Marshal(req)
	if err != nil {
		return inputJSON
	}
	return string(b)
}

// forkDescriptionFallback builds the required Description for fork tool calls
// whose LLM schema omits it (fork_review). Prefers a truncated prompt for
// context; falls back to the child kind.
func forkDescriptionFallback(kind, prompt string) string {
	if p := strings.TrimSpace(prompt); p != "" {
		return truncate(p, 60)
	}
	return kind
}

// stringSlice coerces a decoded JSON value ([]any of strings) to []string,
// dropping non-string entries. Returns nil for anything else.
func stringSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// truncateForDiagnostic caps diagnostic Input/Output fields so a single large tool
// result (e.g. a multi-MB file_read) cannot blow up oracle memory (200-entry
// ring) or the Problems panel UI. Keeps the leading portion + ellipsis, which is
// enough for triage; full payload stays in step.Content via block.appended.
func truncateForDiagnostic(s string) string {
	const cap = 2000
	if len(s) <= cap {
		return s
	}
	return s[:cap] + "... [truncated]"
}

// tryShellExecIntercept routes project.shell_exec / shell.exec calls to the safest backend.
// It returns (output, isErr, intercepted).
func tryShellExecIntercept(
	ctx actor.Context,
	planner actor.Planner,
	call pendingToolCall,
	svcRefs map[string]ref.Ref,
) (string, bool, bool) {
	switch call.CallableID {
	case "shell.exec", "project.shell_exec":
	default:
		return "", false, false
	}
	var req domain.ShellExecReq
	if err := json.Unmarshal([]byte(call.Input), &req); err != nil {
		return "", false, false
	}

	if call.CallableID != "shell.exec" {
		return "", false, false
	}

	cmdStr := shellCommandString(req.Command, req.Args)
	cmdName := extractCommandName(req.Command, req.Args)
	if cmdName == "" {
		return "", false, false
	}

	// Safety check: dangerous commands and rm -rf / are rejected.
	parsedArgs := shellArgs(req.Command, req.Args)
	if err := shell.CheckBashSafety(cmdName, parsedArgs); err != nil {
		return wrapShellExecStdout("", err.Error(), 126), true, true
	}

	// Filesystem-preferred intercepts. find without -name stays in VFS.
	if dest, ok := shellFilesystemIntercepts[cmdName]; ok {
		if cmdName != "find" || hasFindNameFlag(cmdStr) {
			return callFilesystemIntercept(ctx, planner, call, svcRefs, dest.callableID, dest.buildPayload, cmdStr, wrapShellExecStdout)
		}
	}

	// Any remaining command falls through to the shell.exec handler.
	// The safety check above already blocked dangerous commands, and
	// handleExec delegates non-builtins to shell.bash for external
	// binary execution. This allows any safe binary on PATH to run
	// without maintaining an exhaustive allowlist.
	return "", false, false
}

// tryShellIntercept keeps shell.bash backward compatibility by intercepting
// common file operations to project.* callables.
// It returns (output, isErr, intercepted).
func tryShellIntercept(
	ctx actor.Context,
	planner actor.Planner,
	call pendingToolCall,
	svcRefs map[string]ref.Ref,
) (string, bool, bool) {
	if call.CallableID != "shell.bash" {
		return "", false, false
	}
	var req domain.ShellBashReq
	if err := json.Unmarshal([]byte(call.Input), &req); err != nil {
		return "", false, false
	}
	cmd := req.Command
	if cmd == "" {
		return "", false, false
	}
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return "", false, false
	}
	cmdName := filepath.Base(fields[0])
	dest, ok := shellFilesystemIntercepts[cmdName]
	if !ok {
		return "", false, false
	}
	return callFilesystemIntercept(ctx, planner, call, svcRefs, dest.callableID, dest.buildPayload, cmd, wrapShellStdout)
}

// callFilesystemIntercept invokes a project file callable and wraps the result.
// The wrap function determines the response shape (ShellExecResp vs ShellBashResp).
func callFilesystemIntercept(
	ctx actor.Context,
	planner actor.Planner,
	call pendingToolCall,
	svcRefs map[string]ref.Ref,
	callableID string,
	buildPayload func(string) ([]byte, error),
	cmd string,
	wrap func(stdout, stderr string, exitCode int32) string,
) (string, bool, bool) {
	payload, payloadErr := buildPayload(cmd)
	if payloadErr != nil {
		return wrap("", fmt.Sprintf("intercept: %v", payloadErr), 1), true, true
	}
	svcRef, ok := svcRefs["project"]
	if !ok {
		var found bool
		svcRef, found = ctx.LookupService("project")
		if !found {
			return wrap("", fmt.Sprintf("service %q not available", "project"), 1), true, true
		}
	}
	result, err := planner.Call(ctx.Lifecycle(), svcRef, callableID, payload).Await()
	if err != nil {
		return wrap("", fmt.Sprintf("%s: %v", callableID, err), 1), true, true
	}
	stdout := convertInterceptResult(result)
	return wrap(stdout, "", 0), false, true
}

// delegateShellExecToBash runs an allowlisted external binary via shell.bash.
// shell.bash is streaming; this helper consumes the stream silently (no turn
// events) and returns the ShellExecResp-shaped JSON that shell.exec callers
// expect. Turn-event emission is reserved for the direct callStreamingTool
// path so the intercept doesn't double-emit.
func delegateShellExecToBash(
	ctx actor.Context,
	planner actor.Planner,
	call pendingToolCall,
	svcRefs map[string]ref.Ref,
	req domain.ShellExecReq,
) (string, bool, bool) {
	bashReq := domain.ShellBashReq{
		Dir:     req.Dir,
		Timeout: req.Timeout,
	}
	if len(req.Args) > 0 {
		bashReq.Command = req.Command
		bashReq.Args = req.Args
	} else {
		bashReq.Command = req.Command
	}

	payload, err := json.Marshal(bashReq)
	if err != nil {
		return wrapShellExecStdout("", fmt.Sprintf("shell.exec: marshal bash req: %v", err), 1), true, true
	}

	svcRef, ok := svcRefs["shell"]
	if !ok {
		var found bool
		svcRef, found = ctx.LookupService("shell")
		if !found {
			return wrapShellExecStdout("", `service "shell" not available`, 1), true, true
		}
	}

	bashResp, err := consumeShellBashStream(ctx.Lifecycle(), planner, svcRef, payload, req.Timeout)
	if err != nil {
		return wrapShellExecStdout("", fmt.Sprintf("shell.bash: %v", err), 1), true, true
	}
	return wrapShellExecStdout(bashResp.Stdout, bashResp.Stderr, bashResp.ExitCode), false, true
}

// consumeShellBashStream drains the shell.bash stream to completion and
// returns the ShellBashResp assembled from the exit chunk. Used by intercept
// paths that want a unary result without emitting turn events.
func consumeShellBashStream(
	lifecycleCtx context.Context,
	planner actor.Planner,
	svcRef ref.Ref,
	payload []byte,
	reqMs int32,
) (domain.ShellBashResp, error) {
	budget := shellStreamBudget(reqMs)
	callCtx, cancel := context.WithTimeout(lifecycleCtx, budget)
	defer cancel()

	var resp domain.ShellBashResp
	gotExit := false
	onChunk := func(chunkAny any) error {
		chunk, ok := chunkAny.(domain.ShellChunk)
		if !ok {
			return nil
		}
		if chunk.Kind == "exit" {
			resp = domain.ShellBashResp{
				Stdout:                   chunk.Stdout,
				Stderr:                   chunk.Stderr,
				ExitCode:                 chunk.ExitCode,
				Truncated:                chunk.Truncated,
				Interrupted:              chunk.Interrupted,
				ReturnCodeInterpretation: chunk.ReturnCodeInterpretation,
			}
			if chunk.DirWarning != "" && !strings.Contains(resp.Stderr, chunk.DirWarning) {
				resp.Stderr = chunk.DirWarning + "\n" + resp.Stderr
			}
			gotExit = true
			return io.EOF
		}
		return nil
	}
	_, err := planner.Stream(callCtx, svcRef, "shell.bash", payload, onChunk).Await()
	if err != nil && !gotExit {
		return domain.ShellBashResp{}, err
	}
	if !gotExit {
		msg, _ := shellNoExitCause(lifecycleCtx.Err(), callCtx.Err(), budget)
		return domain.ShellBashResp{}, errors.New(msg)
	}
	return resp, nil
}

// extractCommandName returns the binary/command name from a shell request.
// It handles both "go version" (string) and Command="go", Args=["version"].
func extractCommandName(cmd string, args []string) string {
	if cmd == "" {
		return ""
	}
	if len(args) > 0 {
		return filepath.Base(cmd)
	}
	fields, err := flagparse.SplitArgs(cmd)
	if err != nil || len(fields) == 0 {
		return ""
	}
	return filepath.Base(fields[0])
}

// shellArgs returns the arguments portion of a shell command, preferring the
// explicit Args array when provided.
func shellArgs(cmd string, args []string) []string {
	if len(args) > 0 {
		return args
	}
	parsed, err := flagparse.SplitArgs(cmd)
	if err != nil || len(parsed) == 0 {
		return nil
	}
	return parsed[1:]
}

// shellCommandString reconstructs a single command string from the command
// name and optional args array. It is used so payload builders see the full
// invocation regardless of whether the LLM used Command string or Args array.
func shellCommandString(cmd string, args []string) string {
	if len(args) == 0 {
		return cmd
	}
	var b strings.Builder
	b.WriteString(cmd)
	for _, a := range args {
		b.WriteByte(' ')
		if strings.ContainsAny(a, " \t\n\"'") {
			b.WriteByte('"')
			b.WriteString(strings.ReplaceAll(a, `"`, `\"`))
			b.WriteByte('"')
		} else {
			b.WriteString(a)
		}
	}
	return b.String()
}

// hasFindNameFlag reports whether a find command uses -name or -iname.
func hasFindNameFlag(cmd string) bool {
	args, err := flagparse.SplitArgs(cmd)
	if err != nil {
		return false
	}
	for _, a := range args {
		if a == "-name" || a == "-iname" {
			return true
		}
	}
	return false
}

// convertInterceptResult 把拦截调用的返回值转换成字符串。
func convertInterceptResult(result any) string {
	if c := converter.LookupValue(result); c != nil {
		if s, err := c(result); err == nil {
			return s
		}
	}
	return converter.Convert(result)
}

// wrapShellStdout 把拦截结果包装成 ShellBashResp JSON，
// 让 LLM 看到统一的 shell 输出格式。
func wrapShellStdout(stdout, stderr string, exitCode int32) string {
	resp := domain.ShellBashResp{Stdout: stdout, Stderr: stderr, ExitCode: exitCode}
	b, _ := json.Marshal(resp)
	return string(b)
}

// wrapShellExecStdout 把拦截结果包装成 ShellExecResp JSON，
// 与 shell.exec 原生返回值保持一致。
func wrapShellExecStdout(stdout, stderr string, exitCode int32) string {
	resp := domain.ShellExecResp{Stdout: stdout, Stderr: stderr, ExitCode: exitCode}
	b, _ := json.Marshal(resp)
	return string(b)
}

// buildRmPayload 把 rm 命令解析成 FileSystemRmReq。
// shell 中的 rm 是显式删除指令，默认带 force=true 直接执行，不再经过 preview。
func buildRmPayload(cmd string) ([]byte, error) {
	var flags struct {
		Recursive bool   `flag:"r,recursive"`
		Force     bool   `flag:"f,force"`
		Path      string // positional
	}
	if _, err := flagparse.ParseCommand(&flags, cmd); err != nil {
		return nil, err
	}
	req := domain.FileSystemRmReq{Path: flags.Path, Recursive: flags.Recursive, Force: true}
	return json.Marshal(req)
}

// buildGrepPayload 把 grep 命令解析成 FileSystemGrepReq。
func buildGrepPayload(cmd string) ([]byte, error) {
	var flags struct {
		Depth          int32  `flag:"d,depth"`
		IgnoreCase     bool   `flag:"i,ignore-case"`
		InvertMatch    bool   `flag:"v,invert-match"`
		WordRegexp     bool   `flag:"w,word-regexp"`
		ExtendedRegexp bool   `flag:"E,extended-regexp"`
		Context        int32  `flag:"C,context"`
		BeforeContext  int32  `flag:"B,before-context"`
		AfterContext   int32  `flag:"A,after-context"`
		Glob           string `flag:"glob,include"`
		HeadLimit      int32  `flag:"head-limit"`
		OutputMode     string `flag:"output-mode"`
		Multiline      bool   `flag:"multiline"`
		Pattern        string // positional 1
		Path           string // positional 2
	}
	if _, err := flagparse.ParseCommand(&flags, cmd); err != nil {
		return nil, err
	}
	req := domain.FileSystemGrepReq{
		Pattern:       flags.Pattern,
		Path:          flags.Path,
		Depth:         flags.Depth,
		IgnoreCase:    flags.IgnoreCase,
		InvertMatch:   flags.InvertMatch,
		WordRegexp:    flags.WordRegexp,
		Context:       flags.Context,
		BeforeContext: flags.BeforeContext,
		AfterContext:  flags.AfterContext,
		Glob:          flags.Glob,
		HeadLimit:     flags.HeadLimit,
		OutputMode:    flags.OutputMode,
		Multiline:     flags.Multiline,
	}
	return json.Marshal(req)
}

// buildListPayload 把 ls 命令解析成 FileSystemListReq。
func buildListPayload(cmd string) ([]byte, error) {
	args, err := flagparse.SplitArgs(cmd)
	if err != nil {
		return nil, err
	}
	if len(args) > 0 {
		args = args[1:] // skip "ls"
	}
	path := "."
	all := false
	pathSet := false
	dashDashSeen := false
	for _, a := range args {
		if dashDashSeen {
			path = a
			break
		}
		if a == "--" {
			dashDashSeen = true
			continue
		}
		if !strings.HasPrefix(a, "-") {
			if !pathSet {
				path = a
				pathSet = true
			}
			continue
		}
		if a == "-a" || a == "--all" || (len(a) > 2 && a[0] == '-' && a[1] != '-' && strings.ContainsRune(a[1:], 'a')) {
			all = true
		}
	}
	req := domain.FileSystemListReq{Path: path, Depth: 0, All: all}
	return json.Marshal(req)
}

// buildFindPayload 把 find ... -name "*.go" 解析成 FileSystemGlobReq。
// Simple find without -name is not intercepted and falls back to VFS.
func buildFindPayload(cmd string) ([]byte, error) {
	args, err := flagparse.SplitArgs(cmd)
	if err != nil {
		return nil, err
	}
	if len(args) > 0 {
		args = args[1:] // skip "find"
	}
	path := "."
	pattern := "**/*"
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-name" || a == "-iname" {
			if i+1 < len(args) {
				pattern = args[i+1]
				if strings.HasPrefix(pattern, `"`) && strings.HasSuffix(pattern, `"`) {
					pattern = pattern[1 : len(pattern)-1]
				}
				if !strings.Contains(pattern, "/") {
					pattern = "**/" + pattern
				}
				i++ // skip consumed value
			}
			continue
		}
		if !strings.HasPrefix(a, "-") && path == "." {
			path = a
		}
	}
	req := domain.FileSystemGlobReq{Path: path, Pattern: pattern}
	return json.Marshal(req)
}

// isShellNonZeroExit checks whether a tool output is a shell response with a non-zero exit code.
func isShellNonZeroExit(callableID, out string) bool {
	if callableID != "shell.bash" && callableID != "shell.exec" && callableID != "project.shell_exec" {
		return false
	}
	var r struct {
		ExitCode int32 `json:"ExitCode"`
	}
	if err := json.Unmarshal([]byte(out), &r); err == nil && r.ExitCode != 0 {
		return true
	}
	return false
}

// normalizeToolError strips framework/severity prefixes from tool error messages
// and returns the cleaned message plus the appropriate diagnostic severity.
//   - "gospore.handler.error: " is stripped (added by gospore handler wrapper).
//   - "[warning] " is stripped and severity becomes "warning".
//   - Shell non-zero exit codes are treated as warnings.
func normalizeToolError(out string) (string, string) {
	out = strings.TrimPrefix(out, "gospore.handler.error: ")
	if strings.HasPrefix(out, "[warning] ") {
		return strings.TrimPrefix(out, "[warning] "), "warning"
	}
	if isShellNonZeroExit("shell.bash", out) || isShellNonZeroExit("shell.exec", out) || isShellNonZeroExit("project.shell_exec", out) {
		return out, "warning"
	}
	return out, "error"
}

// ask_user 过滤阈值。
const (
	askUserHeaderMinLen   = 3
	askUserQuestionMinLen = 10
	minRepeatedRun        = 4 // ≥4 个连续相同字符视为无意义重复
)

// normalizeAskUserInput 解开 LLM 偶尔产生的双重编码 ask_user 输入
// （questions 被序列化为 JSON 字符串，例如 {"questions":"{\"questions\":[...]}"})，
// 验证最终结构，并返回干净的 {"questions":[...]} JSON。
// 格式不可恢复时返回 error，调用方应将错误回写给 agent。
func normalizeAskUserInput(inputJSON string) (string, error) {
	if inputJSON == "" {
		return "", errors.New("empty ask_user input")
	}
	current := inputJSON
	for depth := 0; depth < 4; depth++ {
		var wrapper struct {
			Questions json.RawMessage `json:"questions"`
		}
		if err := json.Unmarshal([]byte(current), &wrapper); err != nil {
			var str string
			if e2 := json.Unmarshal([]byte(current), &str); e2 == nil {
				current = str
				continue
			}
			return "", fmt.Errorf("cannot parse ask_user input: %w", err)
		}
		if len(wrapper.Questions) == 0 {
			return "", errors.New("missing 'questions' field")
		}
		// questions 被编码为 JSON 字符串 → 递归解包
		var asStr string
		if json.Unmarshal(wrapper.Questions, &asStr) == nil {
			current = asStr
			continue
		}
		// questions 必须是数组
		var asArr []json.RawMessage
		if err := json.Unmarshal(wrapper.Questions, &asArr); err != nil {
			return "", fmt.Errorf("'questions' must be an array: %w", err)
		}
		// 校验每个 question 对象的基本字段
		for i, item := range asArr {
			var q struct {
				Header   string `json:"header"`
				Question string `json:"question"`
				Options  []struct {
					Label string `json:"label"`
				} `json:"options"`
			}
			if err := json.Unmarshal(item, &q); err != nil {
				return "", fmt.Errorf("question[%d] is not a valid object: %w", i, err)
			}
			if q.Header == "" && q.Question == "" {
				return "", fmt.Errorf("question[%d] has neither 'header' nor 'question'", i)
			}
		}
		out, _ := json.Marshal(struct {
			Questions []json.RawMessage `json:"questions"`
		}{Questions: asArr})
		return string(out), nil
	}
	return "", errors.New("ask_user input too deeply nested (encoding loop)")
}

// filterShortAskUserQuestions 剔除 ask_user 输入中低质量的问题。
//
// 判定：
//   - options 为空数组或缺字段 → 丢弃（独立条件，OR）。
//   - header < askUserHeaderMinLen && question < askUserQuestionMinLen → 丢弃（AND）。
//   - header/question/options.label/options.description 含 ≥minRepeatedRun 个连续相同字符 → 丢弃（OR）。
//
// 任一未触发即保留。保留原始 question 对象的所有字段，仅做条目级剔除，
// 不重新构造对象，避免字段丢失。
//
// 返回值：
//   - filteredJSON：过滤后的 input JSON。若没有问题被剔除则原样返回。
//   - allFiltered：所有问题都被剔除（应回写错误让 LLM 重试）。
//   - error：input 不是合法 JSON。
func filterShortAskUserQuestions(inputJSON string) (filteredJSON string, allFiltered bool, err error) {
	if inputJSON == "" {
		return "", true, nil
	}
	var raw struct {
		Questions []json.RawMessage `json:"questions"`
	}
	if err := json.Unmarshal([]byte(inputJSON), &raw); err != nil {
		return "", false, err
	}
	if len(raw.Questions) == 0 {
		return "", true, nil
	}
	type optionItem struct {
		Label       string `json:"label"`
		Description string `json:"description"`
	}
	type parsed struct {
		Header   string       `json:"header"`
		Question string       `json:"question"`
		Options  []optionItem `json:"options"`
	}
	kept := make([]json.RawMessage, 0, len(raw.Questions))
	for _, item := range raw.Questions {
		var q parsed
		if e := json.Unmarshal(item, &q); e != nil {
			// 单条解析失败保留原条目，让后续流程而不是过滤器处理异常。
			kept = append(kept, item)
			continue
		}
		if len(q.Options) == 0 {
			continue
		}
		if hasRepeatedRun(q.Header) || hasRepeatedRun(q.Question) {
			continue
		}
		optRepeated := false
		for _, opt := range q.Options {
			if hasRepeatedRun(opt.Label) || hasRepeatedRun(opt.Description) {
				optRepeated = true
				break
			}
		}
		if optRepeated {
			continue
		}
		if len(q.Header) < askUserHeaderMinLen && len(q.Question) < askUserQuestionMinLen {
			continue
		}
		kept = append(kept, item)
	}
	if len(kept) == 0 {
		return "", true, nil
	}
	if len(kept) == len(raw.Questions) {
		return inputJSON, false, nil
	}
	out, mErr := json.Marshal(struct {
		Questions []json.RawMessage `json:"questions"`
	}{Questions: kept})
	if mErr != nil {
		return "", false, mErr
	}
	return string(out), false, nil
}

// hasRepeatedRun reports whether s contains at least minRepeatedRun consecutive
// identical characters. Uses []rune to correctly handle multi-byte Unicode.
func hasRepeatedRun(s string) bool {
	runes := []rune(s)
	if len(runes) < minRepeatedRun {
		return false
	}
	count := 1
	for i := 1; i < len(runes); i++ {
		if runes[i] == runes[i-1] {
			count++
			if count >= minRepeatedRun {
				return true
			}
		} else {
			count = 1
		}
	}
	return false
}
