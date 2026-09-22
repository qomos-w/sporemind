package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/plan"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/compaction"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/llmclient"
	"github.com/qomos-w/sporemind/pkg/tokenest"
)

// dispatchResult 汇总单次 LLM dispatch 产生的数据。
// 这些数据将在 dispatch 成功后用于更新 step、累积状态、追加消息和状态迁移。
type dispatchResult struct {
	iterText        string
	iterUsage       *domain.UsageData
	pending         []pendingToolCall
	stopReason      string
	reasoningText   string
	reasoningStepID string
}

// dispatchCanceledByPause reports whether a cancellation-shaped dispatch error
// was caused by a user pause rather than a real turn cancel. requestPause
// cancels pauseCtx, and the per-op watchers (lifecycleWithCancel /
// awaitStreamOpen) convert that into context.Canceled before the streaming
// ticker observes pauseRequested; planner cancel rejects with the ctx's own
// error since gospore 9f6ec27, while the bare invoke.ErrCallCancelled
// sentinel survives for an explicit Cancel without a ctx cause. Both must
// classify as pause, or the dispatch loop retries through it and the turn
// keeps running while the composer spins in "pausing". A genuine stop sets
// e.cancelled, which wins over pauseRequested.
func (e *turnEngine) dispatchCanceledByPause(err error) bool {
	if !errors.Is(err, context.Canceled) && !errors.Is(err, invoke.ErrCallCancelled) {
		return false
	}
	e.mu.RLock()
	paused := e.pauseRequested
	e.mu.RUnlock()
	return paused && !e.isCancelled()
}

// dispatchBackoff 返回第 attempt 次重试的退避时长，带 ±20% 抖动以避免
// 多 agent 同时撞到同一 provider 故障时的惊群效应。
func dispatchBackoff(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt >= len(dispatchRetryBackoffs) {
		attempt = len(dispatchRetryBackoffs) - 1
	}
	base := float64(dispatchRetryBackoffs[attempt])
	jitter := 0.8 + rand.Float64()*0.4
	return time.Duration(base * jitter)
}

// rateLimitTerminatesUnitLockedSlot reports whether a 429 rate-limit error
// against the given slot should abort dispatch immediately instead of retrying.
// A unit-locked slot (every candidate pins a concrete unit, no aggregator
// fallback) has no alternative unit to rotate to, so a rate limit is terminal —
// retrying the same rate-limited endpoint cannot succeed. Aggregator-backed
// slots are excluded: they retry so the cooled-down unit is skipped in favor of
// the next pool unit.
func rateLimitTerminatesUnitLockedSlot(slot domain.ModelSlot, err error) bool {
	return extractHTTPStatus(err) == 429 && isUnitLockedSlot(slot)
}

// isStreamCutError reports whether a dispatch error is a mid-stream cut
// (ErrStreamClosed). The aggregator wraps the sentinel with %w, but the error
// crosses the gospore actor boundary as a serialized string, so the Go chain
// is lost by the time it reaches the turn engine — fall back to the sentinel
// message, mirroring the idle-timeout string fallback.
func isStreamCutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, llmclient.ErrStreamClosed) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "stream closed unexpectedly")
}

// isPoolExhaustedError reports whether the aggregator returned a pool-exhausted
// error: every candidate was tried and selectUnitExcluding found no remaining
// healthy unit. The aggregator wraps errPoolExhausted with %w, but the error
// crosses the actor wire as a string, so detect by the sentinel message.
// When the whole pool is rate-limited or quota-exhausted, retrying after a
// short backoff cannot succeed — the cooldown windows (minutes to hours) far
// exceed the dispatch backoff sequence (5-20s). Skipping the retry avoids
// hammering already-overloaded providers and piling fire-and-forget diagnostics
// onto aimanager/oracle/aistats.
func isPoolExhaustedError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "pool exhausted, no failover candidates")
}

// defaultDispatchRetryStrategy 返回默认的 dispatch 重试策略。
// 可重试：429 rate limit、502/503/504 服务端临时错误、idle timeout、网络临时错误。
// 不可重试：用户取消、生命周期取消、4xx 客户端错误（除 429）、prompt too long。
func defaultDispatchRetryStrategy() func(attempt int, err error) (shouldRetry bool, backoff time.Duration) {
	return func(attempt int, err error) (bool, time.Duration) {
		if errors.Is(err, context.Canceled) {
			return false, 0
		}
		// Pool exhausted: the aggregator tried every candidate and none are
		// healthy. Retrying after a 5-20s backoff cannot help when cooldowns
		// last minutes to hours — it only piles diagnostics onto backlogged
		// actors. Let the turn fail; the user can retry after quota resets.
		if isPoolExhaustedError(err) {
			return false, 0
		}
		// prompt too long 由专门的 compaction/截断逻辑处理，不消耗 retry 次数。
		if compaction.IsPromptTooLongError(err) {
			return false, 0
		}
		status := llmclient.ExtractHTTPStatus(err)
		if status != 0 {
			switch status {
			case 429, 502, 503, 504:
				return true, dispatchBackoff(attempt)
			default:
				return false, 0
			}
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return false, 0
		}
		if errors.Is(err, llmclient.ErrStreamIdleTimeout) {
			return true, dispatchBackoff(attempt)
		}
		// Mid-stream cut: the provider dropped the connection. Transient —
		// retry re-opens a fresh stream (aggregator rotation already skipped
		// cooled-down units).
		if isStreamCutError(err) {
			return true, dispatchBackoff(attempt)
		}
		if llmclient.IsRetryableError(err) {
			return true, dispatchBackoff(attempt)
		}
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "stream idle timeout") || strings.Contains(msg, "timeout awaiting response headers") {
			return true, dispatchBackoff(attempt)
		}
		if strings.Contains(msg, "connection reset") ||
			strings.Contains(msg, "connection refused") ||
			strings.Contains(msg, "broken pipe") ||
			strings.Contains(msg, "i/o timeout") ||
			strings.Contains(msg, "tls handshake") {
			return true, dispatchBackoff(attempt)
		}
		return false, 0
	}
}

// truncateDispatchError shortens a dispatch failure for the turn.dispatch_retry
// payload. Provider 429 bodies (quota messages with reset times) are the main
// consumer-visible signal during retry storms; cap them so the event stays
// cheap to ship across the wire (project rule: truncate before transport).
func truncateDispatchError(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if utf8.RuneCountInString(s) > 200 {
		return string([]rune(s)[:200]) + "…"
	}
	return s
}

// phaseDispatch 是 Turn 状态机的第一个阶段：
// 1. 消费 pendingMessages（用户在之前阶段发送的新消息）
// 2. 检查取消信号
// 3. 如有在途 fork_child，先等待它们完成
// 4. 打开 LLM step，调用 runDispatch 与模型交互
// 5. 根据 dispatch 结果记录成功或失败，并迁移到 Audit/Commit
//
// 流程图：
//
//	┌──────────────────────┐
//	│ ingestPendingMessages │ 把用户新消息 stamp 后进 history
//	└──────────┬───────────┘
//	           ↓
//	┌──────────────────────┐
//	│    取消信号检查       │
//	└──────────┬───────────┘
//	           ↓
//	┌──────────────────────┐  有在途子 agent
//	│ pendingChildren > 0 ? │────────→ executeWaitChildren ──→ LoopDispatch
//	└──────────┬───────────┘
//	           ↓ 无
//	┌──────────────────────┐
//	│     openLLMStep       │ 创建 step 并 emit step.opened
//	└──────────┬───────────┘
//	           ↓
//	┌──────────────────────┐
//	│      runDispatch      │ 流式请求 LLM，返回文本/工具/推理等
//	└──────────┬───────────┘
//	           ↓
//	    ┌──────┴──────┐
//	    ↓ err != nil  ↓ err == nil
//	recordDispatchFailure  recordDispatchSuccess
//	    ↓                    ↓
//	返回 err              LoopAudit / LoopCompleted
func (e *turnEngine) phaseDispatch(ctx actor.Context, turnID string) error {
	e.ingestPendingMessages()

	select {
	case <-ctx.Lifecycle().Done():
		return context.Canceled
	case <-e.done:
		return context.Canceled
	default:
	}

	// 防御性：如果上一轮还有同步 fork_child 没回来，先等它们完成再发新一轮 LLM。
	// Async 子 agent 不阻塞 dispatch，由 agent_wait 显式收割。
	if e.hasSyncPendingChildren() {
		if err := e.executeWaitChildren(ctx, turnID); err != nil {
			return err
		}
		e.setLoopState(ctx, turnID, LoopDispatch)
		return nil
	}

	e.logger.Debug("turnEngine: phaseDispatch",
		"history", len(e.history), "pendingMsgs", len(e.pendingMessages))

	// One-shot image recognition for text-only primaries, before the wire
	// history is assembled: unrecognized image blocks are described by a
	// vision unit and the recognition text rides the dispatch as text.
	e.recognizeHistoryImages(ctx)

	// Defer LLM-step open until the first non-reasoning content arrives. This
	// lets the reasoning step (if any) consume a lower Seq so it sorts above
	// text and tool frames on the frontend.
	var llmStep domain.TurnAction

	dispatchHistory := e.buildDispatchHistory(ctx, 16000)

	// Snapshot and reset incremental calibration delta for this dispatch.
	e.lastDeltaCC = e.deltaCharCounts
	e.lastDeltaEst = e.deltaEstimatedTokens
	e.deltaCharCounts = tokenest.CharCounts{}
	e.deltaEstimatedTokens = 0

	var res dispatchResult
	var dispatchErr error
	var retried bool
	// reasoningStepID survives across retry attempts so a retried dispatch
	// reuses the reasoning step opened by a failed attempt instead of
	// orphaning a new one. Sticky: an open failure that streams nothing
	// returns an empty ID and must not clear a previous attempt's seed.
	reasoningStepID := ""

	// A slot that pins a concrete unit has no aggregator fallback — its only
	// recovery path on a transient failure is to keep retrying the same
	// endpoint. Give it a larger retry budget than the aggregator-backed path,
	// which can rotate to a different unit.
	maxRetries := e.maxDispatchRetries
	if isUnitLockedSlot(e.primarySlot) {
		maxRetries = e.maxDispatchRetries + unitSlotRetryBudget
	}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// 每次尝试前检查取消
		select {
		case <-ctx.Lifecycle().Done():
			return context.Canceled
		case <-e.done:
			return context.Canceled
		default:
		}

		if attempt > 0 {
			e.logger.Info("turnEngine: dispatch retry", "attempt", attempt, "maxRetries", maxRetries)
		}

		res.iterText, res.iterUsage, res.pending, res.stopReason,
			res.reasoningText, res.reasoningStepID, dispatchErr =
			e.runDispatch(ctx, turnID, dispatchHistory, &llmStep, reasoningStepID)
		if res.reasoningStepID != "" {
			reasoningStepID = res.reasoningStepID
		}

		if dispatchErr == nil {
			break
		}

		// User pause during streaming: abort immediately, don't retry or treat
		// as failure. Set LoopDispatch so the outer loop's pause check fires.
		// requestPause cancels pauseCtx which propagates to the dispatch
		// stream's startCtx, so the interruption can surface as
		// context.Canceled (e.g. awaitStreamOpen) before the 60ms ticker
		// observes pauseRequested — classify it as a pause, not a cancel.
		if errors.Is(dispatchErr, errPaused) || e.dispatchCanceledByPause(dispatchErr) {
			e.logger.Info("turnEngine: dispatch paused by user mid-stream")
			e.setLoopState(ctx, turnID, LoopDispatch)
			return nil
		}

		// Retry with compaction if the provider rejects the request for being too
		// large. If compaction fails, fall back to aggressive truncation.
		// Only compact when there are no open tool calls to avoid breaking tool_use/tool_result pairs.
		// This does not consume a retry attempt.
		if compaction.IsPromptTooLongError(dispatchErr) && e.openToolCalls == 0 {
			e.logger.Warn("turnEngine: dispatch failed with prompt too long, running compaction",
				"error", dispatchErr.Error())
			if e.onCompact != nil {
				if compactErr := e.onCompact(ctx, turnID, ""); compactErr == nil {
					e.compactionFailures = 0
					dispatchHistory = e.buildDispatchHistory(ctx, 4000)
				} else {
					e.compactionFailures++
					e.logger.Error("turnEngine: compaction failed, retrying with aggressive truncation",
						"error", compactErr.Error())
					e.foldHistory()
					dispatchHistory = e.buildDispatchHistory(ctx, 4000)
				}
			} else {
				e.logger.Warn("turnEngine: dispatch failed with prompt too long, retrying with aggressive truncation",
					"error", dispatchErr.Error())
				e.foldHistory()
				dispatchHistory = e.buildDispatchHistory(ctx, 4000)
			}
			continue
		}

		// 429 rate-limit on a unit-locked slot: stop immediately (no retry).
		// Retrying the same rate-limited endpoint cannot succeed, and the slot
		// has no aggregator to rotate to a different unit. Aggregator-backed
		// slots are NOT stopped here — they retry so the cooled-down unit is
		// skipped in favor of the next pool unit.
		if rateLimitTerminatesUnitLockedSlot(e.primarySlot, dispatchErr) {
			e.logger.Info("turnEngine: dispatch hit rate limit on unit-locked slot, stopping without retry",
				"attempt", attempt, "maxRetries", maxRetries)
			break
		}

		// Mid-stream cut with an open tool_use cannot be retried: replaying
		// the request would break tool_use/tool_result pairing in history.
		if isStreamCutError(dispatchErr) && e.openToolCalls > 0 {
			e.logger.Info("turnEngine: mid-stream cut with open tool calls, not retrying",
				"attempt", attempt, "openToolCalls", e.openToolCalls)
			break
		}

		shouldRetry, backoff := e.dispatchRetryStrategy(attempt, dispatchErr)
		if !shouldRetry || attempt == maxRetries {
			break
		}

		if e.onReportDiagnostic != nil {
			diag := domain.OracleReportDiagnosticReq{
				Severity:   "warning",
				Source:     "dispatch",
				Message:    fmt.Sprintf("dispatch failed (attempt %d/%d), retrying: %v", attempt+1, maxRetries+1, dispatchErr),
				TurnID:     turnID,
				StepID:     llmStep.ID,
				Unit:       e.diagUnit(),
				HttpStatus: extractHTTPStatus(dispatchErr),
			}
			e.onReportDiagnostic(ctx, diag)
		}

		// Surface the retry to the frontend so TurnTail can show a live
		// "retrying (n/m)" indicator during the backoff wait. The truncated
		// error text lets the user see WHY (e.g. 429 quota with reset time)
		// instead of minutes of dead air.
		retried = true
		_ = ctx.EmitEvent("turn", domain.TurnEvent{
			Kind:   domain.TurnDispatchRetry,
			TurnID: turnID,
			Payload: map[string]any{
				"retry":      attempt + 1,
				"maxRetries": maxRetries,
				"error":      truncateDispatchError(dispatchErr),
			},
		})

		// Mid-stream cut retry: the failed attempt streamed partial content
		// into the llm/reasoning steps (live frontend + session step store).
		// Reset both so the regenerated stream replaces instead of
		// concatenating; the backend only ever persists the final attempt.
		if isStreamCutError(dispatchErr) {
			e.resetStreamedSteps(turnID, &llmStep, reasoningStepID)
		}

		select {
		case <-ctx.Lifecycle().Done():
			return context.Canceled
		case <-e.done:
			return context.Canceled
		case <-time.After(backoff):
			// continue
		}
	}

	// Clear the retry indicator once this dispatch iteration resolves
	// (success, failure, or exhausted attempts) so no stale badge lingers.
	if retried {
		_ = ctx.EmitEvent("turn", domain.TurnEvent{
			Kind:   domain.TurnDispatchRetry,
			TurnID: turnID,
			Payload: map[string]any{
				"retry":      0,
				"maxRetries": maxRetries,
			},
		})
	}

	if dispatchErr != nil {
		// 取消（用户停止/生命周期关闭）不算 dispatch 失败：跳过 error step
		// 与 diagnostic，避免 UI 显示 "context canceled"。主循环会让引擎转入
		// LoopCancelled，closeRemainingOpenSteps 会保留已流式输出的文本。
		if errors.Is(dispatchErr, context.Canceled) {
			return dispatchErr
		}
		e.recordDispatchFailure(ctx, turnID, llmStep, res, dispatchErr)
		return dispatchErr
	}

	e.recordDispatchSuccess(ctx, turnID, llmStep, res)
	return nil
}

// ingestPendingMessages 取出 pendingMessages 队列中的用户消息，
// 以及 agent 层面挂起的 pending submits，给它们打戳后追加进 history 和 delta。
func (e *turnEngine) ingestPendingMessages() {
	if flushed := e.flushPendingMessages(); len(flushed) > 0 {
		for i := range flushed {
			e.stamp(&flushed[i])
			e.history = append(e.history, flushed[i])
			e.delta = append(e.delta, flushed[i])
		}
	}
	if e.consumePendingSubmits != nil {
		if msgs := e.consumePendingSubmits(); len(msgs) > 0 {
			for i := range msgs {
				e.stamp(&msgs[i])
				e.history = append(e.history, msgs[i])
				e.delta = append(e.delta, msgs[i])
			}
		}
	}
}

// openLLMStep 创建 LLM step 条目并向订阅者发送 step.opened 事件。
// unit 为本次 dispatch 实际选中的模型，随事件下发供前端逐 step 展示。
func (e *turnEngine) openLLMStep(turnID string, unit domain.ModelUnit) domain.TurnAction {
	llmStep := newStep(&e.stepSeq, turnID, domain.TurnActionLLMCall, "llm")
	e.upsertStep(llmStep)
	e.emitStepEvent(domain.StepEvent{
		Kind:     "step.opened",
		StepID:   llmStep.ID,
		TurnID:   turnID,
		StepType: "text",
		Role:     "assistant",
		Block:    &domain.ContentBlock{Type: domain.ContentBlockText, Text: ""},
		Model:    unit.Model,
		Provider: unit.Provider,
	})
	return llmStep
}

// stepResolvedUnit 返回本次 dispatch 实际使用的模型：优先 aggregator 解析的
// resolvedUnit（在内容流之前到达），否则回退到请求 unit（locked/auto 场景）。
func (e *turnEngine) stepResolvedUnit(s *runDispatchLoopState) domain.ModelUnit {
	if s.resolvedUnit.Model != "" {
		return s.resolvedUnit
	}
	if e.startReq.Input.Unit != nil {
		return *e.startReq.Input.Unit
	}
	return domain.ModelUnit{}
}

// servingUnit returns the unit that served (or was asked to serve) the current
// dispatch: the aggregator-resolved unit when reported, else the requested one.
// Used to attribute telemetry (requestStats, aistats records) to the real
// provider/model instead of the requested unit under aggregator/auto routing.
func (e *turnEngine) servingUnit() domain.ModelUnit {
	if e.dispatchResolvedUnit.Model != "" {
		return e.dispatchResolvedUnit
	}
	if e.startReq.Input.Unit != nil {
		return *e.startReq.Input.Unit
	}
	return domain.ModelUnit{}
}

// diagUnit returns the unit a dispatch diagnostic (oracle problem report)
// should attribute to: the aggregator-resolved serving unit when reported,
// otherwise the requested unit.
func (e *turnEngine) diagUnit() *domain.ModelUnit {
	if u := e.servingUnit(); u.Model != "" || u.Provider != "" {
		return &u
	}
	return nil
}

// unitAttributedIdleTimeout wraps the engine-side stream idle timeout with the
// unit actually serving the dispatch (resolved unit preferred) so retry
// diagnostics name the provider/model that stalled. The endpoint is only
// known inside the aggregator; its own errors already carry it. The sentinel
// stays chained via %w so retry classification keeps matching.
func (e *turnEngine) unitAttributedIdleTimeout(s *runDispatchLoopState) error {
	u := e.stepResolvedUnit(s)
	if u.Provider == "" && u.Model == "" {
		return llmclient.ErrStreamIdleTimeout
	}
	return fmt.Errorf("%w [provider=%s model=%s]", llmclient.ErrStreamIdleTimeout, u.Provider, u.Model)
}

// resetStreamedSteps clears streamed content left by a failed dispatch attempt
// so a retried attempt replaces it rather than concatenating. Emits step.reset
// for the LLM step and the reasoning step (when present); the agent-side step
// store and the live frontend clear their accumulated text. The steps stay
// open — the retry continues streaming into the same step IDs, and only the
// final attempt's content is ever persisted.
func (e *turnEngine) resetStreamedSteps(turnID string, llmStep *domain.TurnAction, reasoningStepID string) {
	if llmStep != nil && llmStep.ID != "" {
		llmStep.Text = ""
		e.upsertStep(*llmStep)
		e.emitStepEvent(domain.StepEvent{Kind: "step.reset", StepID: llmStep.ID, TurnID: turnID})
	}
	if reasoningStepID != "" {
		e.emitStepEvent(domain.StepEvent{Kind: "step.reset", StepID: reasoningStepID, TurnID: turnID})
	}
}

// recordDispatchFailure 在 LLM dispatch 失败后发出 step.error 事件。
func (e *turnEngine) recordDispatchFailure(ctx actor.Context, turnID string, llmStep domain.TurnAction, res dispatchResult, err error) {
	if e.onReportDiagnostic != nil {
		diag := domain.OracleReportDiagnosticReq{
			Severity:   "error",
			Source:     "dispatch",
			Message:    err.Error(),
			TurnID:     turnID,
			StepID:     llmStep.ID,
			Unit:       e.diagUnit(),
			HttpStatus: extractHTTPStatus(err),
		}
		e.onReportDiagnostic(ctx, diag)
	}
	if llmStep.ID != "" {
		e.upsertStep(failStep(llmStep, err.Error()))
		e.emitStepEvent(domain.StepEvent{
			Kind:   "step.error",
			StepID: llmStep.ID,
			TurnID: turnID,
			Error:  err.Error(),
		})
	}
	if res.reasoningStepID != "" {
		e.upsertStep(failStep(domain.TurnAction{ID: res.reasoningStepID, Kind: string(domain.TurnActionReasoning), Title: "reasoning", State: "running"}, err.Error()))
		e.emitStepEvent(domain.StepEvent{
			Kind:   "step.error",
			StepID: res.reasoningStepID,
			TurnID: turnID,
			Error:  err.Error(),
		})
	}
}

// recordDispatchSuccess 在 LLM dispatch 成功后：
// 关闭 LLM step、关闭 reasoning step（如有）、累积使用量和迭代状态、
// 追加 assistant 消息、并迁移 loopState。
func (e *turnEngine) recordDispatchSuccess(ctx actor.Context, turnID string, llmStep domain.TurnAction, res dispatchResult) {
	// A provider may omit usage. Keep a display-only estimate on the LLM step
	// so the frontend can still show in/out, but never put it on res.iterUsage:
	// only provider-reported usage may enter totals, requestStats, budget or
	// compaction decisions.
	displayUsage := res.iterUsage
	if displayUsage == nil {
		displayUsage = e.estimateDisplayUsage(res)
	}
	if llmStep.ID != "" {
		e.closeLLMStep(turnID, llmStep, res.iterText, displayUsage)
	}
	e.closeReasoningStep(turnID, res.reasoningStepID, res.reasoningText)
	e.recordRequestStat(ctx, res)
	e.accumulateDispatchState(res)
	// Update context budget bar with real InputTokens from the LLM usage.
	// Calibrate using incremental actual tokens, comparing the same scope as
	// lastDeltaEst (messages appended since the previous dispatch). Skip the
	// first dispatch because lastDeltaEst covers history text only while the
	// actual InputTokens includes system/tools/hot context overhead.
	if res.iterUsage != nil && res.iterUsage.InputTokens > 0 {
		if e.onContextBudgetUpdated != nil {
			e.onContextBudgetUpdated(res.iterUsage)
		}
		if e.iter > 1 && e.lastActualInputTokens > 0 && e.lastDeltaEst > 0 && e.onCalibrate != nil {
			actualDelta := res.iterUsage.InputTokens - e.lastActualInputTokens
			if actualDelta > 0 {
				e.onCalibrate(e.lastDeltaCC, e.lastDeltaEst, int(actualDelta))
			}
		}
		e.lastActualInputTokens = res.iterUsage.InputTokens
	} else if realInputOccupancy(res.iterUsage) <= 0 {
		// Provider reported no usage. Fall back to a tiktoken estimate so the
		// budget bar still reflects growing context occupancy; the estimate is
		// display-only — it never enters cost/cache/requestStats and is
		// overwritten by the next real usage this provider does report.
		if e.onContextBudgetEstimate != nil {
			e.onContextBudgetEstimate(e.estimatedInputTokens())
		}
	}
	e.finalizeDispatch(ctx, turnID, res)
}

// recordRequestStat builds a TurnRequestStat from the dispatch result and the
// requested unit so the persisted Turn carries per-request telemetry. When a
// cost rate is available from the aistats actor, it is applied to the usage.
func (e *turnEngine) recordRequestStat(ctx actor.Context, res dispatchResult) {
	if res.iterUsage == nil {
		return
	}
	usage := *res.iterUsage
	stat := domain.TurnRequestStat{
		ID:          fmt.Sprintf("%s-%d", e.turnID, len(e.requestStats)+1),
		Usage:       &usage,
		StopReason:  res.stopReason,
		StartedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if u := e.servingUnit(); u.Provider != "" || u.Model != "" {
		stat.Provider = u.Provider
		stat.Model = u.Model
	}
	if res.iterUsage.ReasoningTokens > 0 && stat.StopReason == "" {
		stat.StopReason = "stop"
	}
	if e.onCostRate != nil && stat.Provider != "" && stat.Model != "" {
		if rate, ok := e.onCostRate(ctx, stat.Provider, stat.Model); ok {
			lcu := llmclient.Usage{
				InputTokens:              int(usage.InputTokens),
				OutputTokens:             int(usage.OutputTokens),
				TotalTokens:              int(usage.TotalTokens),
				CacheCreationInputTokens: int(usage.CacheCreationInputTokens),
				CacheReadInputTokens:     int(usage.CacheReadInputTokens),
				ReasoningTokens:          int(usage.ReasoningTokens),
			}
			cost := llmclient.CalculateCost(lcu, rate)
			usage.CostInput = cost.Input
			usage.CostOutput = cost.Output
			usage.CostCacheRead = cost.CacheRead
			usage.CostCacheWrite = cost.CacheWrite
			usage.CostTotal = cost.Total
			stat.Usage = &usage
		}
	}
	e.requestStats = append(e.requestStats, stat)
}

// recordIdleStreamFailure attributes a turn-engine-side stream idle timeout to
// the unit that was actually serving this dispatch. The idle timer firing here
// means the dispatch stream was cancelled before the aggregator could classify
// the failure — a mid-stream cancel is indistinguishable from a benign caller
// hangup on its side, so without this record the availability-cooldown counter
// never advances and every retry re-selects the same stalled unit.
func (e *turnEngine) recordIdleStreamFailure(s *runDispatchLoopState) {
	unit := e.stepResolvedUnit(s)
	if unit.Provider == "" || unit.Model == "" {
		return
	}
	llmclient.RecordFailure(unit.Provider, unit.Model, llmclient.ErrStreamIdleTimeout)
}

// recordIdleTimeout submits a failed-dispatch aistats record when the turn
// engine's idle timer fires. By this point the aggregator stream has been
// cancelled, so the aggregator cannot record this failure itself — a
// mid-stream cancel is indistinguishable from a user pause on its side.
// Recording the timeout here keeps it attributable by error code in the
// model-unit dashboard. Partial usage collected before the stall is included.
func (e *turnEngine) recordIdleTimeout(ctx actor.Context, s *runDispatchLoopState) {
	if e.onStatsRecord == nil || e.workspaceID == "" {
		return
	}
	recID := fmt.Sprintf("%s-timeout-%d", e.turnID, len(e.requestStats)+1)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	record := domain.AIStatsRecord{
		ID:           recID,
		RequestID:    recID,
		WorkspaceID:  e.workspaceID,
		ProjectID:    e.projectID,
		AgentID:      e.agentID,
		SessionID:    e.turnID,
		TurnID:       e.turnID,
		StopReason:   llmclient.StopReasonError,
		ErrorCode:    llmclient.ErrorCodeTimeout,
		ErrorMessage: llmclient.ErrStreamIdleTimeout.Error(),
		StartedAt:    now,
		CompletedAt:  now,
	}
	if u := e.stepResolvedUnit(s); u.Provider != "" || u.Model != "" {
		record.Provider = u.Provider
		record.Model = u.Model
	}
	if s.iterUsage != nil {
		record.Usage = s.iterUsage
	}
	e.onStatsRecord(ctx, record)
}

// maxCompactionFailures is the per-turn compaction circuit breaker. Once
// compaction has failed this many times, shouldCompactAfterDispatch returns
// false for the rest of the turn. Without this, a persistently-failing
// summarizer (e.g. provider timeout) traps the turn in a slow
// "dispatch → compaction-timeout → dispatch" loop where each round burns the
// full compactionSummaryTimeout while the context is never actually reduced,
// making the conversation appear frozen.
const maxCompactionFailures = 1

// shouldCompactAfterDispatch reports whether actual input tokens exceeded the
// agent's configured token budget. The budget is resolved from CompactionPolicy
// and supports both absolute token counts and percentage of context window.
func (e *turnEngine) shouldCompactAfterDispatch(actualInputTokens int64) bool {
	// Circuit breaker: stop attempting auto-compaction after a failure so a
	// dead summarizer cannot stall the turn in a timeout loop. The turn then
	// proceeds normally at the elevated context size.
	if e.compactionFailures >= maxCompactionFailures {
		return false
	}
	// Prefer the live token budget from ContextBudget so the trigger threshold
	// always matches what the budget bar displays. This prevents compaction
	// from firing when the displayed budget is higher (e.g. the aggregator
	// wasn't ready at turn start and the frozen CompiledContext value fell
	// back to the 128k default while the real window is 200k).
	if e.resolveTokenBudget != nil {
		if budget := e.resolveTokenBudget(); budget > 0 {
			return actualInputTokens > int64(budget)
		}
	}
	// Fallback: frozen CompiledContext values from turn start.
	policy := e.startReq.CompiledContext.CompactionPolicy
	windowSize := e.startReq.CompiledContext.ContextWindowSize
	if windowSize <= 0 {
		model := ""
		if e.startReq.Input.Unit != nil {
			model = e.startReq.Input.Unit.Model
		}
		windowSize = int32(tokenest.ModelContextWindow(model))
	}
	budget := compaction.ResolveTokenBudget(policy, windowSize)
	return budget > 0 && actualInputTokens > int64(budget)
}

// closeLLMStep 把 LLM step 标记为完成并发出 step.closed 事件。
func (e *turnEngine) closeLLMStep(turnID string, llmStep domain.TurnAction, text string, usage *domain.UsageData) {
	llmStep.Text = text
	llmStep.State = "completed"
	llmStep.Usage = usage
	e.upsertStep(llmStep)
	e.emitStepEvent(domain.StepEvent{
		Kind:   "step.closed",
		StepID: llmStep.ID,
		TurnID: turnID,
		Usage:  copyUsage(usage),
	})
}

// closeReasoningStep 把 reasoning step 标记为完成并发出 step.closed 事件。
func (e *turnEngine) closeReasoningStep(turnID, reasoningStepID, reasoningText string) {
	if reasoningStepID == "" {
		return
	}
	rs := domain.TurnAction{
		ID:    reasoningStepID,
		Kind:  string(domain.TurnActionReasoning),
		Title: "reasoning",
		Text:  reasoningText,
		State: "completed",
	}
	e.upsertStep(rs)
	e.emitStepEvent(domain.StepEvent{
		Kind:   "step.closed",
		StepID: reasoningStepID,
		TurnID: turnID,
	})
}

// accumulateDispatchState 把本轮 dispatch 的计数器和用量累加到引擎状态。
func (e *turnEngine) accumulateDispatchState(res dispatchResult) {
	e.iter++
	e.actionCount++
	e.iterText = res.iterText
	e.totalUsage = mergeUsage(e.totalUsage, res.iterUsage)
	e.finalText += res.iterText
	if e.onUsageUpdated != nil {
		e.onUsageUpdated(e.totalUsage)
	}
}

// finalizeDispatch 根据 dispatch 结果原子性地完成 history 写入和状态迁移。
// 将原来的 appendDispatchAssistantMessage + transitionAfterDispatch 合并，
// 消除两个函数对同一状态做独立决策导致的不一致（如孤儿 tool_result）。
func (e *turnEngine) finalizeDispatch(ctx actor.Context, turnID string, res dispatchResult) {
	// Case 1: 正常 tool_use — 写 assistant + tool_use 块，迁移到 Audit。
	if len(res.pending) > 0 && res.stopReason == "tool_use" {
		asstContent := make([]domain.ContentBlock, 0, 1+len(res.pending))
		if res.iterText != "" {
			asstContent = append(asstContent, domain.ContentBlock{Type: domain.ContentBlockText, Text: res.iterText})
		}
		for i, pt := range res.pending {
			res.pending[i].RawToolCall = fmt.Sprintf(`{"id":%q,"type":"function","function":{"name":%q,"arguments":%q}}`, pt.ID, pt.LLMName, pt.Input)
			// 部分推理渠道经中转网关下发 tool_call arguments 时会产生两类损坏：
			// 字符串字面量内的换行以原始字节下发（JSON 要求转义，可状态机转义
			// 无损修复），以及结构性缺字（引号/大括号被吞，无法修复）。修复失败
			// 时标记 MalformedArgs：调用不执行、以错误结果回流——绝不允许带着
			// {} 静默执行后返回 exit 0，模型必须看到调用本身失败。
			if !json.Valid([]byte(pt.Input)) {
				switch {
				case strings.TrimSpace(pt.Input) == "":
					res.pending[i].Input = "{}"
				default:
					if repaired := escapeRawControlChars(pt.Input); json.Valid([]byte(repaired)) {
						e.logger.Warn("turnEngine: repaired tool_call arguments containing raw control bytes",
							"toolName", pt.LLMName, "toolID", pt.ID, "inputLen", len(pt.Input))
						res.pending[i].Input = repaired
					} else {
						res.pending[i].MalformedArgs = true
					}
				}
			}
			if res.pending[i].MalformedArgs {
				e.logger.Warn("turnEngine: tool_call arguments malformed beyond repair; failing call without executing",
					"toolName", pt.LLMName, "toolID", pt.ID, "inputLen", len(pt.Input))
				if e.onReportDiagnostic != nil {
					e.onReportDiagnostic(ctx, domain.OracleReportDiagnosticReq{
						Severity:      "warning",
						Source:        "tool_call",
						Message:       fmt.Sprintf("Tool %s produced unrecoverable JSON arguments (%d bytes); call failed without executing", pt.LLMName, len(pt.Input)),
						TurnID:        turnID,
						CallableID:    pt.CallableID,
						TargetService: pt.ServiceName,
						ToolUseID:     pt.ID,
						Input:         truncateForDiagnostic(pt.Input),
						Unit:          modelUnitPtrToOracle(e.startReq.Input.Unit),
						RawData:       res.pending[i].RawToolCall,
					})
				}
				// History/lowering 安全：非法 JSON 不能原样进 assistant 历史，
				// 执行路径也不会再读取这个 Input（MalformedArgs 先行拦截）。
				res.pending[i].Input = "{}"
			}
			asstContent = append(asstContent, domain.ContentBlock{
				Type:      domain.ContentBlockToolUse,
				ToolUseID: pt.ID,
				ToolName:  pt.LLMName,
				Input:     res.pending[i].Input,
			})
		}
		e.openToolCalls += len(res.pending)
		toolUseIDs := make([]string, len(res.pending))
		for i, pt := range res.pending {
			toolUseIDs[i] = fmt.Sprintf("%s=%s", pt.LLMName, pt.ID)
		}
		e.logger.Debug("turnEngine: finalizeDispatch assistant message", "turnID", turnID, "toolUseIDs", toolUseIDs)
		// Mid-turn compaction: run BEFORE appending the assistant(tool_use)
		// message. onCompact rebuilds e.history from a.compileMessages(true),
		// which reads a.steps — and the current iteration's LLM step has not
		// been flushed to a.steps yet (phaseCommit runs later). If we append
		// first, the rebuild silently discards the tool_use, leaving orphaned
		// tool_results that sanitizeMessages then drops — so the LLM never
		// sees its tool outputs and may re-request them in a loop.
		//
		// When the provider returns usage, use the real input occupancy. When
		// it does not (some channels omit usage entirely), fall back to a
		// tiktoken estimate so compaction can still fire — otherwise a usage-
		// less provider would never trigger compaction and the context would
		// grow unbounded.
		inputOccupancy := realInputOccupancy(res.iterUsage)
		if inputOccupancy <= 0 {
			inputOccupancy = e.estimatedInputTokens()
		}
		if inputOccupancy > 0 &&
			e.shouldCompactAfterDispatch(inputOccupancy) && e.onCompact != nil {
			if compactErr := e.onCompact(ctx, turnID, ""); compactErr != nil {
				e.compactionFailures++
				e.logger.Error("turnEngine: pre-tool compact failed; circuit breaker will skip further auto-compaction this turn",
					"error", compactErr.Error(), "inputTokens", inputOccupancy)
			} else {
				e.compactionFailures = 0
			}
		}
		e.appendMessage(domain.ChatMessage{
			ID:               ctx.NewID().String(),
			Role:             domain.ChatRoleAssistant,
			ReasoningContent: res.reasoningText,
			Content:          asstContent,
			Usage:            copyUsage(res.iterUsage),
		})
		// Loop guard: detect near-repetitive reasoning/text across dispatch
		// iterations before tools run. A confirmed repeat queues a randomized
		// correction (soft) or fails the turn (hard, after ignored corrections).
		// On hard break pendingCalls stays empty so no orphan tools execute.
		if e.runLoopGuard(ctx, turnID, res.reasoningText, res.iterText) {
			return
		}
		e.pendingCalls = res.pending
		e.batches = nil
		e.batchIdx = 0
		e.emptyToolUseRetries = 0
		e.setLoopState(ctx, turnID, LoopAudit)
		return
	}

	// Case 2: 模型声称 tool_use 但未解析出任何工具调用 → 重试 dispatch。
	if res.stopReason == "tool_use" {
		e.emptyToolUseRetries++
		if e.emptyToolUseRetries <= 3 {
			e.logger.Warn("turnEngine: tool_use stop reason but zero tool calls parsed, retrying dispatch",
				"retry", e.emptyToolUseRetries)
			e.setLoopState(ctx, turnID, LoopDispatch)
			return
		}
		e.logger.Error("turnEngine: tool_use stop reason with zero tool calls after max retries, completing turn")
	}

	// Case 3: 普通文本完成→ 写 assistant 文本消息，完成 turn。
	e.emptyToolUseRetries = 0
	e.mu.Lock()
	e.acceptingInjects = false
	e.mu.Unlock()
	if res.reasoningText != "" || res.iterText != "" {
		e.appendMessage(domain.ChatMessage{
			ID:               ctx.NewID().String(),
			Role:             domain.ChatRoleAssistant,
			ReasoningContent: res.reasoningText,
			Content:          []domain.ContentBlock{{Type: domain.ContentBlockText, Text: res.iterText}},
			Usage:            copyUsage(res.iterUsage),
		})
	}

	// Unfinished task reminder: if there are still pending or in_progress
	// tasks, append a reminder assistant message and continue the loop so
	// the LLM acts on it instead of ending the turn. The reminder fires at
	// most once per turn to avoid infinite loops.
	if e.onUnfinishedTasks != nil && !e.taskReminderEmitted {
		unfinished := e.onUnfinishedTasks()
		if len(unfinished) > 0 {
			e.taskReminderEmitted = true
			e.appendMessage(domain.ChatMessage{
				ID:    ctx.NewID().String(),
				Role:  domain.ChatRoleAssistant,
				Usage: copyUsage(res.iterUsage),
				Content: []domain.ContentBlock{{
					Type: domain.ContentBlockText,
					Text: buildUnfinishedTaskReminderText(unfinished),
				}},
			})
			e.mu.Lock()
			e.acceptingInjects = true
			e.mu.Unlock()
			e.setLoopState(ctx, turnID, LoopDispatch)
			return
		}
	}

	// Turn-end forced IO dispatch: before the turn reaches its terminal
	// state, check whether the active agent owns a doing task card with an
	// IO contract (data.io). If so, reroute into LoopForcedDispatch instead
	// of completing. At most one attempt per turn regardless of outcome.
	if e.maybeEnterForcedDispatch(ctx, turnID) {
		return
	}

	e.setLoopState(ctx, turnID, LoopCompleted)
}

// escapeRawControlChars 修复字符串字面量内部的裸控制字节（\n、\t 等）——
// JSON 规范要求字符串内控制字符必须转义；字面量之间的换行是合法空白，保持
// 原样。某些推理渠道经中转网关下发 tool_call arguments 时会丢失字符串内换行
// 的转义。按状态机区分字面量内外，仅当修复后能通过 json.Valid 才会被调用方
// 采用；结构本身已破损（如引号转义丢失）时修复无效，回退原逻辑。
func escapeRawControlChars(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 16)
	inStr := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !inStr {
			if c == '"' {
				inStr = true
			}
			b.WriteByte(c)
			continue
		}
		switch c {
		case '\\':
			// 已转义字符（\" \\ \n 等）原样成对拷贝。
			if i+1 < len(s) {
				b.WriteByte(c)
				i++
				b.WriteByte(s[i])
			}
		case '"':
			inStr = false
			b.WriteByte(c)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 0x20 {
				b.WriteString(fmt.Sprintf(`\u%04x`, c))
			} else {
				b.WriteByte(c)
			}
		}
	}
	return b.String()
}

// runDispatchLoopState 保存单次流式 dispatch 循环的可变状态。
// 通过指针传给 chunk handler，使它们能直接更新共享累加器，
// 无需通过复杂元组返回值。
type runDispatchLoopState struct {
	turnID          string
	iterText        string
	reasoningText   strings.Builder
	reasoningStepID string
	iterUsage       *domain.UsageData
	pending         []pendingToolCall
	pendingByID     map[string]*pendingToolCall
	stopReason      string
	// resolvedUnit is the concrete unit the aggregator actually selected for
	// this dispatch, captured from the resolved_unit chunk. Empty until reported.
	resolvedUnit domain.ModelUnit
	textBuf      strings.Builder
	reasoningBuf strings.Builder
	llmStep      *domain.TurnAction
	flushDeltas  func() error
	toolEffects  map[string]domain.EffectKind
	toolByName   map[string]domain.ToolSpec
}

// chunkHandler 处理一种 chunk 类型并更新循环状态。
type chunkHandler func(e *turnEngine, ctx actor.Context, chunk domain.AggregatorChunk, s *runDispatchLoopState) error

// decodeChunk 把 aggregator 收到的值解码为类型化的 chunk。
func decodeChunk(value any) (domain.AggregatorChunk, error) {
	switch cv := value.(type) {
	case domain.AggregatorChunk:
		return cv, nil
	case []byte:
		var chunk domain.AggregatorChunk
		if err := json.Unmarshal(cv, &chunk); err != nil {
			return domain.AggregatorChunk{}, fmt.Errorf("recv: %w", err)
		}
		return chunk, nil
	default:
		return domain.AggregatorChunk{}, fmt.Errorf("recv: unexpected chunk type %T", value)
	}
}

// routeStreamChunk 解码一个流值并分派到对应的 chunk handler。resetIdle 仅在
// 真实内容 chunk（非 ResolvedUnit 元数据）时触发：ResolvedUnit 在首 token
// 之前到达，不应把首 token 等待窗口缩到 StreamChunkIdleTimeout。
func (e *turnEngine) routeStreamChunk(ctx actor.Context, value any, s *runDispatchLoopState, resetIdle func()) error {
	chunk, err := decodeChunk(value)
	if err != nil {
		return err
	}
	if chunk.Kind != domain.AggregatorChunkResolvedUnit {
		resetIdle()
	}
	h, ok := map[domain.AggregatorChunkKind]chunkHandler{
		domain.AggregatorChunkText:              (*turnEngine).handleTextChunk,
		domain.AggregatorChunkReasoning:         (*turnEngine).handleReasoningChunk,
		domain.AggregatorChunkUsage:             (*turnEngine).handleUsageChunk,
		domain.AggregatorChunkToolUseStart:      (*turnEngine).handleToolUseStartChunk,
		domain.AggregatorChunkToolUseInputDelta: (*turnEngine).handleToolUseInputDeltaChunk,
		domain.AggregatorChunkToolUseComplete:   (*turnEngine).handleToolUseCompleteChunk,
		domain.AggregatorChunkStop:              (*turnEngine).handleStopChunk,
		domain.AggregatorChunkResolvedUnit:      (*turnEngine).handleResolvedUnitChunk,
	}[chunk.Kind]
	if !ok {
		return fmt.Errorf("unknown chunk kind: %v", chunk.Kind)
	}
	return h(e, ctx, chunk, s)
}

// handleTextChunk 处理 LLM 返回的文本增量。首次调用时按需打开 LLM step，
// 确保 reasoning step（如有）已经以更小的 Seq 排在前端 timeline 上方。
func (e *turnEngine) handleTextChunk(_ actor.Context, chunk domain.AggregatorChunk, s *runDispatchLoopState) error {
	if s.llmStep.ID == "" {
		*s.llmStep = e.openLLMStep(s.turnID, e.stepResolvedUnit(s))
	}
	s.iterText += chunk.Text
	s.llmStep.Text = s.iterText
	s.textBuf.WriteString(chunk.Text)
	return s.flushDeltas()
}

// handleReasoningChunk 处理 reasoning 增量；首次收到时创建 reasoning step。
func (e *turnEngine) handleReasoningChunk(_ actor.Context, chunk domain.AggregatorChunk, s *runDispatchLoopState) error {
	if s.reasoningStepID == "" {
		if err := s.flushDeltas(); err != nil {
			return err
		}
		rs := newStep(&e.stepSeq, s.turnID, domain.TurnActionReasoning, "reasoning")
		s.reasoningStepID = rs.ID
		e.upsertStep(rs)
		unit := e.stepResolvedUnit(s)
		e.emitStepEvent(domain.StepEvent{
			Kind:     "step.opened",
			StepID:   rs.ID,
			TurnID:   s.turnID,
			StepType: "reasoning",
			Role:     "assistant",
			Model:    unit.Model,
			Provider: unit.Provider,
		})
	}
	s.reasoningText.WriteString(chunk.Text)
	s.reasoningBuf.WriteString(chunk.Text)
	return s.flushDeltas()
}

// handleUsageChunk 处理用量块；通常出现在流末尾。
func (e *turnEngine) handleUsageChunk(_ actor.Context, chunk domain.AggregatorChunk, s *runDispatchLoopState) error {
	if err := s.flushDeltas(); err != nil {
		return err
	}
	if chunk.Usage != nil {
		copyU := *chunk.Usage
		s.iterUsage = &copyU
	}
	return nil
}

// handleToolUseStartChunk 在 LLM 开始一个 tool_use 时创建 pendingToolCall。
// 如果 LLM step 尚未打开（纯 tool-call 回复，无文本），在此按需打开。
func (e *turnEngine) handleToolUseStartChunk(_ actor.Context, chunk domain.AggregatorChunk, s *runDispatchLoopState) error {
	if err := s.flushDeltas(); err != nil {
		return err
	}
	if s.llmStep.ID == "" {
		*s.llmStep = e.openLLMStep(s.turnID, e.stepResolvedUnit(s))
	}
	nameKey := strings.ToLower(chunk.ToolName)
	spec, ok := s.toolByName[nameKey]
	ptc := pendingToolCall{
		ID:      chunk.ToolUseID,
		LLMName: chunk.ToolName,
	}
	if ok {
		ptc.CallableID = spec.CallableID
		ptc.EffectKind = s.toolEffects[nameKey]
		ptc.ServiceName = spec.ServiceName
	} else {
		ptc.UnknownTool = true
	}
	s.pending = append(s.pending, ptc)
	s.pendingByID[chunk.ToolUseID] = &s.pending[len(s.pending)-1]
	return nil
}

// handleToolUseInputDeltaChunk 追加 tool_use input 的增量片段。
func (e *turnEngine) handleToolUseInputDeltaChunk(_ actor.Context, chunk domain.AggregatorChunk, s *runDispatchLoopState) error {
	if ptc, ok := s.pendingByID[chunk.ToolUseID]; ok {
		ptc.Input += chunk.InputDelta
	}
	return nil
}

// handleToolUseCompleteChunk 在 tool_use 完成时用最终 input 覆盖累加值。
func (e *turnEngine) handleToolUseCompleteChunk(_ actor.Context, chunk domain.AggregatorChunk, s *runDispatchLoopState) error {
	if ptc, ok := s.pendingByID[chunk.ToolUseID]; ok {
		ptc.Input = chunk.Input
	}
	return nil
}

// handleStopChunk 处理 stop 原因，通常标志着一轮模型输出结束。
func (e *turnEngine) handleStopChunk(_ actor.Context, chunk domain.AggregatorChunk, s *runDispatchLoopState) error {
	if err := s.flushDeltas(); err != nil {
		return err
	}
	s.stopReason = chunk.StopReason
	return nil
}

// handleResolvedUnitChunk 捕获聚合器为本次 dispatch 实际选中的 unit，
// 并通过 onResolvedUnit 回调让 agent 更新 status.Unit（反映真实执行的 model）。
// 该 chunk 在内容流之前到达，使顶栏在首 token 前即可显示正确模型。
func (e *turnEngine) handleResolvedUnitChunk(_ actor.Context, chunk domain.AggregatorChunk, s *runDispatchLoopState) error {
	if chunk.ResolvedUnit == nil || chunk.ResolvedUnit.Model == "" {
		return nil
	}
	s.resolvedUnit = *chunk.ResolvedUnit
	e.dispatchResolvedUnit = *chunk.ResolvedUnit
	if e.onResolvedUnit != nil {
		e.onResolvedUnit(*chunk.ResolvedUnit)
	}
	return nil
}

// buildDispatchRequest 从 CompiledContext、turn 输入和已清理历史中组装
// aiaggregator.dispatch 的请求载荷。Hot context 被追加到 system prompt
// 末尾，与 CompiledPrompt 视图的展示顺序一致。
func (e *turnEngine) buildDispatchRequest(ctx actor.Context, turnID string, history []domain.ChatMessage) domain.SendSessionMessageReq {
	// Goal block is dynamic (its Confirmed state may change during a turn), so
	// refresh it on every dispatch before appending hot context to the system prompt.
	hotContext := e.startReq.CompiledContext.HotContext
	if e.onGoalBlockRefresh != nil {
		if freshGoal := e.onGoalBlockRefresh(ctx); freshGoal != nil {
			hotContext = refreshGoalBlock(hotContext, *freshGoal)
		}
	}

	systemPrompt, systemBlocks := assembleSystemPrompt(e.startReq.CompiledContext.Instructions, hotContext)

	req := domain.SendSessionMessageReq{
		SessionID:       turnID,
		WorkspaceID:     e.workspaceID,
		ProjectID:       e.projectID,
		AgentID:         e.agentID,
		TurnID:          turnID,
		Unit:            e.startReq.Input.Unit,
		Title:           e.startReq.Input.Title,
		System:          systemPrompt,
		SystemBlocks:    systemBlocks,
		Messages:        history,
		Tools:           e.tools,
		ThinkingBudget:  e.startReq.Input.ThinkingBudget,
		ReasoningEffort: e.startReq.Input.ReasoningEffort,
		SlotKind:        "primary",
	}
	// Turn-end forced IO dispatch: the contract pins the tool surface to the
	// declared callable and forces ToolChoice onto it (one extra LLM round
	// trip whose only job is argument generation). See phaseForcedDispatch.
	if e.forcedIO != nil {
		applyForcedIOOverrides(&req, e.forcedIO.ToolSpec)
	}
	return req
}

// buildToolIndex 返回工具的效果类型查找表和名字/callable 查找表。
// 名字查找支持 LLM 可能发出的全部拼写：声明名（连字符形式）、点号 callable ID
// （提示词里的写法）、下划线形式（旧写法），以及唯一时的裸名；均不区分大小写。
// 效果表与名字表共用同一套键，见 toolAliasKeys。
func (e *turnEngine) buildToolIndex() (map[string]domain.EffectKind, map[string]domain.ToolSpec) {
	effects := toolEffectIndex(e.tools)
	counts := bareNameCounts(e.tools)
	byName := make(map[string]domain.ToolSpec, len(e.tools)*4)
	for _, t := range e.tools {
		for _, k := range toolAliasKeys(t, counts) {
			if _, exists := byName[k]; !exists {
				byName[k] = t
			}
		}
	}
	return effects, byName
}

// hasInteractionSubmitTool reports whether the current tool set includes
// plan_submit or goal_submit, i.e. the LLM is expected to generate a plan or
// goal interpretation during this dispatch. The caller uses this to select an
// extended idle timeout so deep reasoning is not killed by StreamIdleTimeout.
func (e *turnEngine) hasInteractionSubmitTool(toolByName map[string]domain.ToolSpec) bool {
	if _, ok := toolByName["plan_submit"]; ok {
		return true
	}
	if _, ok := toolByName["goal_submit"]; ok {
		return true
	}
	if _, ok := toolByName["goal_card_submit"]; ok {
		return true
	}
	return false
}

// openDispatchStream 创建 plan node、启动它，然后等待聚合器产出第一个流
// 元素（resolved-unit chunk 或终止性 handler 错误），最后返回接收通道、
// 取消函数、空闲定时器和 peek 到的首个元素。
//
// 首个元素的等待是真正的 commit boundary：聚合器在 stream open 失败时
// （如 http 403 配额耗尽）会把 handler 错误作为第一个元素送达，而不是通过
// node.Start 返回。把它归入 open 失败，selectDispatchStream 才能切换到
// slot 候选链中的下一个 candidate；一旦首个 chunk 到手，流即确认打开，
// 之后不再回退。出错时内部会自行清理；成功时调用方负责清理返回的 node/timer。
func (e *turnEngine) openDispatchStream(ctx actor.Context, aggRef ref.Ref, sessReq domain.SendSessionMessageReq) (plan.Node, <-chan plan.RecvResult, context.CancelFunc, *time.Timer, plan.RecvResult, error) {
	planner := ctx.Planner()
	if planner == nil {
		return nil, nil, nil, nil, plan.RecvResult{}, fmt.Errorf("planner not available")
	}

	model := ""
	if sessReq.Unit != nil {
		model = sessReq.Unit.Model
	}
	e.logger.Debug("turnEngine: creating plan", "model", model, "messages", len(sessReq.Messages), "tools", len(sessReq.Tools))
	node, err := planner.Plan(aggRef, "aiaggregator.dispatch", sessReq)
	if err != nil {
		return nil, nil, nil, nil, plan.RecvResult{}, fmt.Errorf("plan: %w", err)
	}
	e.currentNode = node

	// Use lifecycleWithCancel so eng.cancel() (which closes e.done) also
	// cancels startCtx. Without this bridge, node.Start can block forever
	// if the aggregator hangs, and waitExit() deadlocks the default loop.
	startCtx, cancelAll := e.lifecycleWithCancel(ctx.Lifecycle())
	const idleTimeout = domain.StreamIdleTimeout
	idleTimer := time.NewTimer(idleTimeout)

	cleanup := func(err error) (plan.Node, <-chan plan.RecvResult, context.CancelFunc, *time.Timer, plan.RecvResult, error) {
		_ = node.Stop()
		_ = ctx.Destroy(node.Ref())
		e.currentNode = nil
		cancelAll()
		idleTimer.Stop()
		return nil, nil, nil, nil, plan.RecvResult{}, err
	}

	if err := node.Start(startCtx); err != nil {
		return cleanup(fmt.Errorf("start: %w", err))
	}

	recvCh := plan.RecvChan(startCtx, node)
	first, err := awaitStreamOpen(recvCh, idleTimer, startCtx.Done())
	if err != nil {
		// Attribute the engine-side pre-open idle timeout to the requested
		// unit (the resolved-unit chunk has not arrived yet; the aggregator's
		// own error carries the endpoint). Aggregator handler errors already
		// carry their own unit context — pass those through untouched.
		if errors.Is(err, llmclient.ErrStreamIdleTimeout) && sessReq.Unit != nil {
			err = fmt.Errorf("%w [provider=%s model=%s, awaiting stream open]", err, sessReq.Unit.Provider, sessReq.Unit.Model)
		}
		return cleanup(err)
	}
	return node, recvCh, cancelAll, idleTimer, first, nil
}

// awaitStreamOpen 等待 dispatch 流的第一个元素。聚合器在流确认打开后立即
// 发出 resolved-unit chunk；若 stream open 失败（如 http 403 quota），
// handler 错误会作为第一个元素送达。返回错误意味着尚未产出任何内容，
// 调用方可以安全地回退到下一个 candidate。
func awaitStreamOpen(recvCh <-chan plan.RecvResult, idleTimer *time.Timer, done <-chan struct{}) (plan.RecvResult, error) {
	select {
	case r, ok := <-recvCh:
		if !ok {
			select {
			case <-done:
				return plan.RecvResult{}, context.Canceled
			default:
			}
			return plan.RecvResult{}, errors.New("dispatch stream closed before stream open")
		}
		if r.Err != nil {
			if errors.Is(r.Err, io.EOF) {
				return plan.RecvResult{}, errors.New("dispatch stream ended before stream open")
			}
			return plan.RecvResult{}, r.Err
		}
		return r, nil
	case <-idleTimer.C:
		return plan.RecvResult{}, llmclient.ErrStreamIdleTimeout
	case <-done:
		return plan.RecvResult{}, context.Canceled
	}
}

// newRunDispatchLoopState 构造流式 dispatch 循环状态，包括 flushDeltas 闭包。
// flushDeltas 把缓冲的文本/reasoning 增量刷成 step block.delta 事件。
func (e *turnEngine) newRunDispatchLoopState(turnID string, llmStep *domain.TurnAction, ctx actor.Context, toolEffects map[string]domain.EffectKind, toolByName map[string]domain.ToolSpec) *runDispatchLoopState {
	s := &runDispatchLoopState{
		turnID:      turnID,
		pendingByID: make(map[string]*pendingToolCall),
		llmStep:     llmStep,
		toolEffects: toolEffects,
		toolByName:  toolByName,
	}

	s.flushDeltas = func() error {
		if s.reasoningBuf.Len() > 0 {
			text := s.reasoningBuf.String()
			s.reasoningBuf.Reset()
			e.emitStepEvent(domain.StepEvent{
				Kind:       "block.delta",
				StepID:     s.reasoningStepID,
				TurnID:     turnID,
				BlockIndex: 0,
				Delta:      text,
			})
		}
		if s.textBuf.Len() > 0 && llmStep.ID != "" {
			text := s.textBuf.String()
			s.textBuf.Reset()
			e.emitStepEvent(domain.StepEvent{
				Kind:       "block.delta",
				StepID:     llmStep.ID,
				TurnID:     turnID,
				BlockIndex: 0,
				Delta:      text,
			})
		}
		if err := e.flushEvents(ctx); err != nil {
			e.logger.Error("turnEngine: dispatch flush failed", "err", err)
		}
		if err := e.flushTurnEvents(ctx); err != nil {
			e.logger.Error("turnEngine: dispatch turn flush failed", "err", err)
		}
		return nil
	}

	return s
}

// streamOpener opens a dispatch stream for a single resolved target. Extracted
// as a parameter so the candidate-selection boundary (try each candidate until
// one opens, then commit) can be tested in isolation with a fake opener.
//
// "Open" means the stream is confirmed open: the opener peeks the first stream
// item and returns it as first. An aggregator handler error delivered as the
// first item (e.g. aiaggregator.dispatch: stream open: http 403) is an open
// failure — no content was emitted, so the candidate loop falls back.
type streamOpener func(ctx actor.Context, aggRef ref.Ref, sessReq domain.SendSessionMessageReq) (plan.Node, <-chan plan.RecvResult, context.CancelFunc, *time.Timer, plan.RecvResult, error)

// selectDispatchStream implements the agent-layer candidate-chain fallback:
// it tries each resolved target's stream opener in candidate order, and the
// first to succeed wins. This is the boundary between "can fall back to the
// next candidate" (pre-stream-open) and "committed to this stream" (once open,
// the caller consumes it and never returns here). A candidate that fails to
// open its stream is skipped in favor of the next; if every candidate fails,
// the last error is returned so the caller surfaces it.
//
// The unit attached to a unit-kind candidate is pinned onto the request; an
// empty unit (aggregator/auto candidate) clears any unit on the base request
// so the aggregator's own strategy selects the model. Only a unit-locked slot
// (pure unit selection, no aggregator/auto fallback) sets UnitPinned: its
// stream-open failures surface to the caller (retry same unit) instead of
// rotating to another pool unit or falling through to the next candidate.
// Any slot that routes through an aggregator — including a unit manually
// picked from an aggregator's pool ([unit@agg, auto]) and learned soft
// affinity — keeps full failover.
func selectDispatchStream(ctx actor.Context, e *turnEngine, targets []dispatchTarget, sessReq domain.SendSessionMessageReq, open streamOpener) (plan.Node, <-chan plan.RecvResult, context.CancelFunc, *time.Timer, plan.RecvResult, error) {
	var (
		node      plan.Node
		recvCh    <-chan plan.RecvResult
		cancelAll context.CancelFunc
		idleTimer *time.Timer
		first     plan.RecvResult
		openErr   error
	)
	unitLocked := isUnitLockedSlot(e.primarySlot)
	for _, tgt := range targets {
		req := sessReq
		if tgt.unit.Model != "" {
			u := tgt.unit
			req.Unit = &u
			req.UnitPinned = unitLocked
		} else {
			// Aggregator/auto candidate: clear any unit pinned on the base
			// request. buildDispatchRequest copies TurnInput.Unit, which mirrors
			// the slot's first unit-kind candidate — leaving it in place would
			// re-pin the just-failed unit as the first attempt on every
			// fallback target instead of letting the pool auto-pick.
			req.Unit = nil
			req.UnitPinned = false
		}
		node, recvCh, cancelAll, idleTimer, first, openErr = open(ctx, tgt.aggRef, req)
		if openErr == nil {
			break
		}
		e.logger.Warn("turnEngine: dispatch candidate failed, trying next", "error", openErr)
		// Record the failed unit in the route chain so the next dispatch
		// prefers units that did not just fail. Only unit-kind candidates
		// carry a concrete unit worth demoting; aggregator/auto failures
		// rotate internally with nothing to demote.
		if e.onRouteUnitFailed != nil && tgt.unit.Model != "" && tgt.unit.Provider != "" {
			e.onRouteUnitFailed(tgt.unit)
		}
		// A unit-locked slot must not fall through to the next unit candidate —
		// that would switch models on a non-aggregator selection. Surface the
		// failure so phaseDispatch's retry loop retries the same unit.
		if unitLocked {
			break
		}
	}
	return node, recvCh, cancelAll, idleTimer, first, openErr
}

// runDispatch 发起一次 aiaggregator.dispatch 调用并以流式方式消费 chunk。
// 返回本轮 assistant 文本、用量增量、待执行工具调用、stop reason、
// reasoning 文本、reasoning step ID，以及可能发生的错误。
//
// 流式循环流程图：
//
//	┌───────────────────────────────┐
//	│   构建 SendSessionMessageReq  │
//	│   构建 tool 查找表            │
//	└───────────┬───────────────────┘
//	            ↓
//	┌─────────────────────────┐
//	│   openDispatchStream    │ Plan → Start → RecvChan
//	└───────────┬─────────────┘
//	            ↓
//	┌─────────────────────────┐
//	│  for { select { ... } } │
//	│  recvCh: decode → handler │
//	│  ticker.C: flushDeltas    │
//	│  idleTimer.C: Deadline    │
//	│  e.done / ctx.Done: Cancel│
//	└───────────┬─────────────┘
//	            ↓
//	┌─────────────────────────┐
//	│   最终 flushDeltas       │
//	│   返回累加结果            │
//	└─────────────────────────┘
func (e *turnEngine) runDispatch(
	ctx actor.Context,
	turnID string,
	history []domain.ChatMessage,
	llmStep *domain.TurnAction,
	reasoningSeed string,
) (string, *domain.UsageData, []pendingToolCall, string, string, string, error) {
	sessReq := e.buildDispatchRequest(ctx, turnID, history)
	toolEffects, toolByName := e.buildToolIndex()
	// A fresh attempt must not inherit the previous attempt's resolved unit:
	// pre-open failures would be attributed to a unit the aggregator may have
	// already rotated away from.
	e.dispatchResolvedUnit = domain.ModelUnit{}

	// When plan_submit or goal_submit is among the available tools the LLM is
	// expected to generate a plan or goal interpretation before calling the
	// tool. This can involve deep reasoning that runs well past the normal
	// 3-minute idle budget, so use the extended timeout for the whole
	// dispatch (the idle timer is reset on every received chunk).
	idleTimeout := domain.StreamIdleTimeout
	if e.hasInteractionSubmitTool(toolByName) {
		idleTimeout = domain.PlanGoalStreamIdleTimeout
		e.logger.Debug("turnEngine: dispatch uses extended idle timeout", "turnID", turnID, "idleTimeout", idleTimeout)
	}

	// Resolve the primary slot's candidate chain and try each in order. A
	// candidate whose aggregator is unreachable (plan/start failure) is
	// skipped in favor of the next; once a stream starts we commit to it.
	targets := e.resolveTargets(ctx, e.primarySlot)
	if len(targets) == 0 {
		return "", nil, nil, "", "", "", fmt.Errorf("dispatch: no model target available")
	}
	node, recvCh, cancelAll, idleTimer, first, openErr := selectDispatchStream(ctx, e, targets, sessReq, e.openDispatchStream)
	if openErr != nil {
		return "", nil, nil, "", "", "", openErr
	}
	defer func() {
		e.currentNode = nil
		_ = node.Stop()
		_ = ctx.Destroy(node.Ref())
	}()
	defer cancelAll()
	defer idleTimer.Stop()

	// openDispatchStream creates the idle timer with the default StreamIdleTimeout.
	// Reset it to the possibly-extended duration so the first idle window
	// matches the reset behavior in the loop below.
	if idleTimeout != domain.StreamIdleTimeout {
		idleTimer.Stop()
		idleTimer.Reset(idleTimeout)
	}

	s := e.newRunDispatchLoopState(turnID, llmStep, ctx, toolEffects, toolByName)
	// Seed the reasoning step from a previous (failed) attempt so a retry
	// reuses the same step instead of orphaning a new one.
	s.reasoningStepID = reasoningSeed

	const deltaFlushInterval = 60 * time.Millisecond
	ticker := time.NewTicker(deltaFlushInterval)
	defer ticker.Stop()

	// resetIdleTimer fires on every received chunk. The first chunk already
	// arrived (the initial timer window of idleTimeout covered the first-token
	// wait), so subsequent gaps use StreamChunkIdleTimeout. The wider window
	// permits legitimate mid-stream reasoning pauses while bounding dead streams.
	resetIdleTimer := func() {
		idleTimer.Stop()
		idleTimer.Reset(domain.StreamChunkIdleTimeout)
	}

	// The opener peeked the stream's first item to confirm the stream actually
	// opened (typically the resolved-unit chunk); route it through the normal
	// chunk path before entering the receive loop.
	if err := e.routeStreamChunk(ctx, first.Value, s, resetIdleTimer); err != nil {
		_ = s.flushDeltas()
		return s.iterText, s.iterUsage, s.pending, s.stopReason, s.reasoningText.String(), s.reasoningStepID, err
	}

loop:
	for {
		select {
		case r, ok := <-recvCh:
			if !ok {
				break loop
			}
			if errors.Is(r.Err, io.EOF) {
				break loop
			}
			if r.Err != nil {
				_ = s.flushDeltas()
				return s.iterText, s.iterUsage, s.pending, s.stopReason, s.reasoningText.String(), s.reasoningStepID, r.Err
			}
			if err := e.routeStreamChunk(ctx, r.Value, s, resetIdleTimer); err != nil {
				_ = s.flushDeltas()
				return s.iterText, s.iterUsage, s.pending, s.stopReason, s.reasoningText.String(), s.reasoningStepID, err
			}

		case <-ticker.C:
			// Check if the user requested a pause while streaming. Aborting
			// here lets the outer loop's pause check fire immediately instead
			// of waiting for the full LLM response (which can take 10-30s).
			e.mu.Lock()
			paused := e.pauseRequested
			e.mu.Unlock()
			if paused {
				cancelAll()
				_ = s.flushDeltas()
				return s.iterText, s.iterUsage, s.pending, s.stopReason, s.reasoningText.String(), s.reasoningStepID, errPaused
			}
			if err := s.flushDeltas(); err != nil {
				return s.iterText, s.iterUsage, s.pending, s.stopReason, s.reasoningText.String(), s.reasoningStepID, err
			}
		case <-idleTimer.C:
			cancelAll()
			_ = s.flushDeltas()
			e.recordIdleStreamFailure(s)
			e.recordIdleTimeout(ctx, s)
			return s.iterText, s.iterUsage, s.pending, s.stopReason, s.reasoningText.String(), s.reasoningStepID, e.unitAttributedIdleTimeout(s)
		case <-e.done:
			_ = s.flushDeltas()
			return s.iterText, s.iterUsage, s.pending, s.stopReason, s.reasoningText.String(), s.reasoningStepID, context.Canceled
		case <-ctx.Lifecycle().Done():
			e.logger.Info("turnEngine: runDispatch lifecycle done")
			_ = s.flushDeltas()
			return s.iterText, s.iterUsage, s.pending, s.stopReason, s.reasoningText.String(), s.reasoningStepID, context.Canceled
		}
	}

	_ = s.flushDeltas()
	e.logger.Debug("turnEngine: runDispatch done",
		"stopReason", s.stopReason,
		"tools", len(s.pending),
		"textLen", len(s.iterText),
		"reasoning", len(s.reasoningText.String()))
	return s.iterText, s.iterUsage, s.pending, s.stopReason, s.reasoningText.String(), s.reasoningStepID, nil
}

// compiledSummary 把所有非空 summary segment 文本拼接起来。
func (e *turnEngine) compiledSummary() string {
	if len(e.summarySegments) == 0 {
		return ""
	}
	var parts []string
	for _, seg := range e.summarySegments {
		if seg.Text != "" {
			parts = append(parts, seg.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// buildDispatchHistory 构造发送给 LLM 的有效消息列表。
// 组装顺序：summary 前缀 → 历史消息 → sanitize 修复。
//
// 注意：不要再按 summary 覆盖范围过滤历史。compileMessages() 在 turn 启动前
// 已经按 step index 正确过滤了被 summary 覆盖的 steps；这里的 message Idx 与
// step index 不对齐，二次过滤会拆散 assistant(tool_use)/tool(tool_result) 对。
// dispatchImageKeepLast bounds how many screenshot-sized data URLs ride one
// dispatch: a computeruse loop taking many shots in a single turn would
// otherwise ship every one of them on every subsequent dispatch (context
// explosion, provider image caps). Older current-turn images degrade to the
// placeholder+ref — the model can always re-recognize them via recognize_image.
const dispatchImageKeepLast = 2

func (e *turnEngine) buildDispatchHistory(ctx actor.Context, toolResultLimit int) []domain.ChatMessage {
	summary := e.compiledSummary()

	result := prependSummaryMessages(summary)

	totalImages := 0
	for i := range e.history {
		if e.history[i].Role != domain.ChatRoleUser {
			continue
		}
		for _, b := range e.history[i].Content {
			if b.Type == domain.ContentBlockImage {
				totalImages++
			}
		}
	}
	seenImages := 0

	for _, msg := range e.history {
		msgCopy := msg
		if msg.Role == "tool" && toolResultLimit > 0 {
			msgCopy.Content = make([]domain.ContentBlock, len(msg.Content))
			copy(msgCopy.Content, msg.Content)
			for j := range msgCopy.Content {
				if msgCopy.Content[j].Type == domain.ContentBlockToolResult && len(msgCopy.Content[j].Text) > toolResultLimit {
					msgCopy.Content[j].Text = msgCopy.Content[j].Text[:toolResultLimit] + "... [truncated]"
				}
			}
		}
		// Text-only dispatch: replace recognized image blocks with their
		// recognition text so the primary model sees the image content as
		// text. Blocks without recognition text stay untouched and fall
		// through to the aggregator's strip path. Vision primaries never
		// enter this branch. The msg:<id> handle lets the model
		// re-recognize with a task-specific prompt via recognize_image.
		if e.dispatchImagesAsText && msg.Role == domain.ChatRoleUser {
			for j := range msg.Content {
				if msg.Content[j].Type == domain.ContentBlockImage && msg.Content[j].RecognitionText != "" {
					if &msgCopy.Content[j] == &msg.Content[j] {
						msgCopy.Content = make([]domain.ContentBlock, len(msg.Content))
						copy(msgCopy.Content, msg.Content)
					}
					msgCopy.Content[j] = domain.ContentBlock{
						Type: domain.ContentBlockText,
						Text: recognizedImageWireText(msg.Content[j].RecognitionText, msg.ID, j),
					}
				}
			}
		}
		// Image budget: once the remaining current-turn images exceed the
		// keep-last budget, older image blocks (whatever survived the
		// text-only replacement above) degrade to placeholder+ref.
		if msg.Role == domain.ChatRoleUser {
			for j := range msgCopy.Content {
				if msgCopy.Content[j].Type != domain.ContentBlockImage {
					continue
				}
				keep := seenImages >= totalImages-dispatchImageKeepLast
				seenImages++
				if keep {
					continue
				}
				if &msgCopy.Content[j] == &msg.Content[j] {
					msgCopy.Content = make([]domain.ContentBlock, len(msg.Content))
					copy(msgCopy.Content, msg.Content)
				}
				msgCopy.Content[j] = domain.ContentBlock{
					Type: domain.ContentBlockText,
					Text: "[image omitted to bound request size]" + "\n[image ref: msg:" + msg.ID + ":" + strconv.Itoa(j) + "]",
				}
			}
		}
		result = append(result, msgCopy)
	}

	result = sanitizeMessages(result)

	// Debug logging: trace full message role sequence and tool_use / tool_result IDs.
	msgSummary := make([]string, len(result))
	for i, m := range result {
		switch m.Role {
		case domain.ChatRoleAssistant:
			var parts []string
			for _, b := range m.Content {
				switch b.Type {
				case domain.ContentBlockText:
					if b.Text != "" {
						parts = append(parts, "text")
					}
				case domain.ContentBlockToolUse:
					parts = append(parts, fmt.Sprintf("tool_use:%s=%s", b.ToolName, b.ToolUseID))
				}
			}
			if len(parts) == 0 {
				msgSummary[i] = "assistant(empty)"
			} else {
				msgSummary[i] = "assistant(" + strings.Join(parts, ",") + ")"
			}
		case "tool":
			var ids []string
			for _, b := range m.Content {
				if b.Type == domain.ContentBlockToolResult {
					ids = append(ids, b.ToolUseID)
				}
			}
			msgSummary[i] = "tool(results=" + strings.Join(ids, ",") + ")"
		default:
			textLen := 0
			for _, b := range m.Content {
				textLen += len(b.Text)
			}
			msgSummary[i] = fmt.Sprintf("%s(textLen=%d)", m.Role, textLen)
		}
	}
	e.logger.Debug("turnEngine: buildDispatchHistory complete", "total", len(result), "sequence", msgSummary)
	return result
}

// prependSummaryMessages 当 summary 非空时返回一对 summary 引导消息。
func prependSummaryMessages(summary string) []domain.ChatMessage {
	if summary == "" {
		return nil
	}
	return []domain.ChatMessage{
		{Role: "user", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: summary}}},
		{Role: "assistant", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "Understood. I have the context from our previous conversation."}}},
	}
}

// buildHotContextMessage filters hot context blocks to non-empty text and
// wraps them in a single user message. Returns nil if no content remains,
// so callers can skip injection cleanly. Currently only exercised by tests; hot
// context is appended to the system prompt in production dispatch.
func buildHotContextMessage(hot []domain.ContentBlock) *domain.ChatMessage {
	var hotBlocks []domain.ContentBlock
	for _, block := range hot {
		if block.Text != "" {
			hotBlocks = append(hotBlocks, block)
		}
	}
	if len(hotBlocks) == 0 {
		return nil
	}
	msg := domain.ChatMessage{Role: domain.ChatRoleUser, Content: hotBlocks}
	return &msg
}

// goalBlockPrefix identifies the goal block in the hot context slice.
const goalBlockPrefix = "## Active Goal"

// refreshGoalBlock replaces the frozen goal block (identified by its
// "## Active Goal" prefix) in the hot context with a freshly-built version.
// If no existing goal block is found, the fresh block is appended.
func refreshGoalBlock(hot []domain.ContentBlock, fresh domain.ContentBlock) []domain.ContentBlock {
	out := make([]domain.ContentBlock, 0, len(hot)+1)
	replaced := false
	for _, block := range hot {
		if strings.HasPrefix(block.Text, goalBlockPrefix) {
			out = append(out, fresh)
			replaced = true
			continue
		}
		out = append(out, block)
	}
	if !replaced {
		out = append(out, fresh)
	}
	return out
}

// sanitizeMessages 修复 LLM 协议违规：
// 1. 丢弃没有对应 assistant tool_use 的 tool 消息；
// 2. 把匹配的 tool result 拉到对应 assistant 消息后面；
// 3. 删除末尾只有 tool_use 但没有 tool result 的 assistant 消息。
func sanitizeMessages(msgs []domain.ChatMessage) []domain.ChatMessage {
	if len(msgs) == 0 {
		return msgs
	}
	var out []domain.ChatMessage
	for _, m := range msgs {
		switch m.Role {
		case "tool":
			if hasMatchingToolCall(out, m) {
				out = append(out, m)
			}
		default:
			out = append(out, m)
		}
	}

	// Reorder: 把匹配的 tool result 前移到对应 assistant 消息之后。
	for i := 0; i < len(out); i++ {
		if out[i].Role != "assistant" || !hasToolUses(out[i]) || nextIsToolResult(out, i) {
			continue
		}
		var matched []int
		for j := i + 1; j < len(out); j++ {
			if out[j].Role == "tool" && toolResultsMatch(out[i], out[j]) {
				matched = append(matched, j)
			}
		}
		if len(matched) == 0 {
			continue
		}
		toolResults := make([]domain.ChatMessage, len(matched))
		for k := len(matched) - 1; k >= 0; k-- {
			idx := matched[k]
			toolResults[k] = out[idx]
			out = append(out[:idx], out[idx+1:]...)
		}
		insertIdx := i + 1
		out = append(out, make([]domain.ChatMessage, len(toolResults))...)
		copy(out[insertIdx+len(toolResults):], out[insertIdx:])
		copy(out[insertIdx:], toolResults)
		i += len(toolResults)
	}

	// After reorder, matching tool_results sit immediately after their
	// assistant. Strip any tool_use blocks that still lack a matching
	// tool_result (orphans). This handles both trailing and non-trailing
	// cases: an assistant with partially-resolved tool_uses is no longer
	// the last message (existing results follow it), but orphaned
	// tool_use blocks remain and would cause a protocol error.
	for i := 0; i < len(out); {
		if out[i].Role != "assistant" || !hasToolUses(out[i]) {
			i++
			continue
		}
		// Collect tool_use IDs covered by immediately following tool messages.
		covered := make(map[string]bool)
		j := i + 1
		for j < len(out) && out[j].Role == "tool" {
			for _, b := range out[j].Content {
				if b.Type == domain.ContentBlockToolResult && b.ToolUseID != "" {
					covered[b.ToolUseID] = true
				}
			}
			j++
		}

		var clean []domain.ContentBlock
		hasOrphan := false
		for _, b := range out[i].Content {
			if b.Type == domain.ContentBlockToolUse && b.ToolUseID != "" && !covered[b.ToolUseID] {
				hasOrphan = true
				continue
			}
			clean = append(clean, b)
		}

		if hasOrphan {
			if len(clean) == 0 {
				// All tool_uses orphaned: strip assistant and its tool_results.
				out = append(out[:i], out[j:]...)
				continue
			}
			out[i].Content = clean
		}
		i++
	}
	return out
}

// hasToolUses 判断消息是否包含 tool_use 块。
func hasToolUses(m domain.ChatMessage) bool {
	for _, b := range m.Content {
		if b.Type == domain.ContentBlockToolUse {
			return true
		}
	}
	return false
}

// toolResultsMatch 判断 assistant 消息的 tool_use 与 tool 消息的 tool_result 是否匹配。
func toolResultsMatch(asst, msg domain.ChatMessage) bool {
	for _, ab := range asst.Content {
		if ab.Type != domain.ContentBlockToolUse {
			continue
		}
		for _, tb := range msg.Content {
			if tb.Type == domain.ContentBlockToolResult && tb.ToolUseID == ab.ToolUseID {
				return true
			}
		}
	}
	return false
}

// hasMatchingToolCall 在历史中往前查找是否有 assistant 消息匹配这个 tool result。
func hasMatchingToolCall(out []domain.ChatMessage, m domain.ChatMessage) bool {
	for i := len(out) - 1; i >= 0; i-- {
		if out[i].Role == "assistant" && toolResultsMatch(out[i], m) {
			return true
		}
	}
	return false
}

// nextIsToolResult 判断指定位置的下一条消息是否是 tool role。
func nextIsToolResult(msgs []domain.ChatMessage, idx int) bool {
	if idx+1 >= len(msgs) {
		return false
	}
	return msgs[idx+1].Role == "tool"
}

// mergeUsage 把单轮用量累加到累计用量中。
func mergeUsage(total, delta *domain.UsageData) *domain.UsageData {
	if delta == nil {
		return total
	}
	if total == nil {
		cp := *delta
		return &cp
	}
	total.InputTokens += delta.InputTokens
	total.OutputTokens += delta.OutputTokens
	total.CacheCreationInputTokens += delta.CacheCreationInputTokens
	total.CacheReadInputTokens += delta.CacheReadInputTokens
	total.TotalTokens = total.InputTokens + total.OutputTokens + total.CacheCreationInputTokens + total.CacheReadInputTokens
	return total
}

// estimateDisplayUsage estimates tokens only for the visible LLM step when a
// provider omits usage. The result must never be used for persistence,
// telemetry or cost/cache statistics. It MAY drive display-only in/out and the
// compaction trigger fallback (see estimatedInputTokens).
func (e *turnEngine) estimateDisplayUsage(res dispatchResult) *domain.UsageData {
	inputTokens := e.estimatedInputTokens()
	outputTokens := int64(tokenest.EstimateTokens(res.iterText))
	outputTokens += int64(tokenest.EstimateTokens(res.reasoningText))
	return &domain.UsageData{
		InputTokens:           inputTokens,
		OutputTokens:          outputTokens,
		TotalTokens:           inputTokens + outputTokens,
		EstimatedPromptTokens: inputTokens,
	}
}

// estimatedInputTokens returns a tiktoken-based estimate of the current input
// context (instructions + hot context + tools + compiled history). It is the
// fallback used when a provider returns no usage at all, so that the compaction
// trigger and budget display can still react. Real provider usage always wins
// over this estimate; callers must never let it overwrite a real value.
func (e *turnEngine) estimatedInputTokens() int64 {
	var inputTokens int64
	if inst := e.startReq.CompiledContext.Instructions; inst != nil {
		for _, p := range inst.Base {
			inputTokens += int64(tokenest.EstimateTokens(p))
		}
		for _, p := range inst.Resolved {
			inputTokens += int64(tokenest.EstimateTokens(p))
		}
	}
	for _, block := range e.startReq.CompiledContext.HotContext {
		inputTokens += int64(tokenest.EstimateTokens(block.Text))
	}
	for _, msg := range e.history {
		for _, b := range msg.Content {
			inputTokens += int64(tokenest.EstimateTokens(b.Text))
			inputTokens += int64(tokenest.EstimateTokens(b.Input))
		}
		inputTokens += int64(tokenest.EstimateTokens(msg.ReasoningContent))
	}
	for _, t := range e.tools {
		inputTokens += int64(tokenest.EstimateTokens(t.Description))
		inputTokens += int64(tokenest.EstimateTokens(t.InputSchema))
	}
	return inputTokens
}

// extractHTTPStatus returns the HTTP status code embedded in err if it (or any
// wrapped cause) is an llmclient.HTTPError; otherwise it falls back to parsing
// the serialized error string.
func extractHTTPStatus(err error) int32 {
	return int32(llmclient.ExtractHTTPStatus(err))
}
