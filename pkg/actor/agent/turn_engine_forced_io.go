package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/protocol"
)

// 回合终局强制 IO toolcall（IO 卡）。
//
// 任务卡可声明 IO 契约：
//
//	data.io:
//	  callable: project.report_status   # dotted callable ID
//	  description: 报告本次回合的产出摘要  # 可选，指导参数生成
//
// 当拥有该卡（data.ownerAgentId = 本 agent）且卡 status=doing 时，agent 的
// 回合在自然终局（文本完成、无待执行工具）前强制插入一个 dispatch 阶段：
//
//	Dispatch → Audit → Execute → … → (文本完成) → ForcedDispatch → Completed
//	                                                  │
//	                                                  ├─ 一次 ToolChoice 钉死的
//	                                                  │  额外 LLM 往返（唯一任务：
//	                                                  │  生成 callable 入参）
//	                                                  ├─ Normalize+Validate 入参
//	                                                  ├─ 执行 callable
//	                                                  └─ task_outputs+done 落卡
//
// 关键约束：宿主不可凭空合成参数——参数必须来自这一次真实的 LLM 往返
// （llmclient.ToolChoice 全 provider 支持，agent 侧首次使用）。失败语义
// （LLM 未产出合法参数 / callable 执行失败 / 落卡失败）：回合标记失败原因，
// 卡保持 doing，下一回合重试。每回合至多尝试一次（forcedIOAttempted），
// 防止 IO 卡常驻 doing 造成回合死循环。

// forcedIOContract 承载一次回合终局强制 IO dispatch 的全部决策输入。
// 由 agent 层从卡片 frontmatter + 拓扑注册表解析（见 agent_forced_io.go）。
type forcedIOContract struct {
	CardID      string                  // 声明契约的任务卡 ID
	CallableID  string                  // dotted callable ID（路由/执行）
	Description string                  // 卡声明的 description，注入参数生成指令
	ToolSpec    domain.ToolSpec         // LLM 面工具规格（含深度入参 schema）
	Layout      *protocol.RequestLayout // 入参布局；nil = 无 ReqSchemaID，跳过布局校验
}

// maybeEnterForcedDispatch 在回合自然终局（finalizeDispatch Case 3）调用：
// 查询活动 agent 的 IO 契约，有则转入 LoopForcedDispatch。返回 true 表示
// 调用方不得再迁移到 LoopCompleted（本函数已接管状态迁移，包括解析失败
// 直接 failTurn 的情况——IO 契约是回合义务，静默跳过会让强制输出凭空消失）。
func (e *turnEngine) maybeEnterForcedDispatch(ctx actor.Context, turnID string) bool {
	if e.forcedIOAttempted || e.onForcedIOContract == nil || e.isCancelled() {
		return false
	}
	contract, err := e.onForcedIOContract(ctx)
	if err != nil {
		e.failTurn(ctx, turnID, fmt.Errorf("forced IO contract resolution failed: %w", err))
		return true
	}
	if contract == nil {
		return false
	}
	e.forcedIO = contract
	e.forcedIOAttempted = true
	e.logger.Info("turnEngine: entering forced IO dispatch",
		"turnID", turnID, "card", contract.CardID, "callable", contract.CallableID)
	e.setLoopState(ctx, turnID, LoopForcedDispatch)
	return true
}

// phaseForcedDispatch 执行回合终局强制 IO toolcall：
// 一次 ToolChoice 钉死的 LLM 往返生成参数 → Normalize+Validate → 执行
// callable → 结果写回任务卡（task_outputs + doing→done）→ LoopCompleted。
//
// 任何失败（未产出合法参数 / 执行失败 / 落卡失败）都返回错误，由 run() 的
// switch 统一 failTurn：回合带失败原因结束，卡保持 doing 供下一回合重试。
// 用户暂停不消耗本次尝试：清掉契约覆盖回到 LoopDispatch，回合在其自然
// 终局会再次进入强制分支。
func (e *turnEngine) phaseForcedDispatch(ctx actor.Context, turnID string) error {
	contract := e.forcedIO
	if contract == nil {
		// 防御：状态机不该在无契约时进入本阶段。
		e.setLoopState(ctx, turnID, LoopCompleted)
		return nil
	}
	defer func() { e.forcedIO = nil }()

	select {
	case <-ctx.Lifecycle().Done():
		return context.Canceled
	case <-e.done:
		return context.Canceled
	default:
	}

	// 注入参数生成指令。强制 ToolChoice 只保证"调用这个工具"；语义上该传
	// 什么由卡片 description + 回合上下文共同决定，指令把这一点显式化。
	e.appendMessage(domain.ChatMessage{
		ID:   ctx.NewID().String(),
		Role: domain.ChatRoleUser,
		Content: []domain.ContentBlock{{
			Type: domain.ContentBlockText,
			Text: forcedIOInstructionText(*contract),
		}},
	})

	var llmStep domain.TurnAction
	dispatchHistory := e.buildDispatchHistory(ctx, 16000)

	iterText, iterUsage, pending, stopReason, reasoningText, reasoningStepID, dispatchErr :=
		e.runDispatch(ctx, turnID, dispatchHistory, &llmStep, "")
	res := dispatchResult{
		iterText:        iterText,
		iterUsage:       iterUsage,
		pending:         pending,
		stopReason:      stopReason,
		reasoningText:   reasoningText,
		reasoningStepID: reasoningStepID,
	}

	// 用户暂停：不消耗尝试。回到正常 Dispatch；回合自然终局会重试强制分支。
	if dispatchErr != nil && (errors.Is(dispatchErr, errPaused) || e.dispatchCanceledByPause(dispatchErr)) {
		e.forcedIOAttempted = false
		e.logger.Info("turnEngine: forced IO dispatch paused by user; will re-attempt at turn end", "turnID", turnID)
		e.setLoopState(ctx, turnID, LoopDispatch)
		return nil
	}
	if dispatchErr != nil {
		// Genuine cancel is a lifecycle event, not an IO failure — mirror
		// phaseDispatch: no step.error/diagnostic; run() classifies it into
		// LoopCancelled (pause-induced cancels were handled above).
		if errors.Is(dispatchErr, context.Canceled) {
			return dispatchErr
		}
		e.recordDispatchFailure(ctx, turnID, llmStep, res, dispatchErr)
		return fmt.Errorf("forced IO dispatch for card %q: %w", contract.CardID, dispatchErr)
	}

	// 成功往返：关闭 step、记账用量（不复用 recordDispatchSuccess——它会经
	// finalizeDispatch 走正常 tool_use/终局迁移，与本阶段的终局语义冲突）。
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

	// 校验强制往返的产物：恰好一个对约定工具的调用、入参过布局校验。
	// 任一不满足 = LLM 未产出合法参数 → 回合失败，卡留 doing 可重试。
	call, cerr := forcedIOPendingCall(*contract, res.pending)
	if cerr != nil {
		e.reportDiagnosticIfSet(ctx, "forced_io", cerr.Error(), turnID)
		return fmt.Errorf("forced IO dispatch for card %q: %w", contract.CardID, cerr)
	}
	validated, aerr := validateForcedIOInput(contract.Layout, call.Input)
	if aerr != nil {
		e.reportDiagnosticIfSet(ctx, "forced_io", aerr.Error(), turnID)
		return fmt.Errorf("forced IO dispatch for card %q: %w", contract.CardID, aerr)
	}

	// 写入 assistant(tool_use)。工具结果在执行后立即补上，两条消息原子成对
	// 追加，不产生孤儿 tool_use；openToolCalls 维持 0（compaction 门不受影响）。
	asstContent := make([]domain.ContentBlock, 0, 2)
	if res.iterText != "" {
		asstContent = append(asstContent, domain.ContentBlock{Type: domain.ContentBlockText, Text: res.iterText})
	}
	asstContent = append(asstContent, domain.ContentBlock{
		Type:      domain.ContentBlockToolUse,
		ToolUseID: call.ID,
		ToolName:  call.LLMName,
		Input:     validated,
	})
	e.appendMessage(domain.ChatMessage{
		ID:               ctx.NewID().String(),
		Role:             domain.ChatRoleAssistant,
		ReasoningContent: res.reasoningText,
		Content:          asstContent,
		Usage:            copyUsage(res.iterUsage),
	})

	// 为强制调用建 tool_call step（与 phaseAudit 的正常路径同构），
	// 保证前端时间线与 turn 记录完整。
	toolStep := newStep(&e.stepSeq, turnID, domain.TurnActionToolCall, call.LLMName)
	toolStep.CallableID = contract.CallableID
	toolStep.TargetService = contract.ToolSpec.ServiceName
	toolStep.Text = validated
	toolStep.Input = validated
	toolStep.ToolUseID = call.ID
	toolStep.Meta = call.ID
	e.upsertStep(toolStep)
	e.emitStepEvent(domain.StepEvent{
		Kind:     "step.opened",
		StepID:   toolStep.ID,
		TurnID:   turnID,
		StepType: "tool_call",
		Role:     "assistant",
		Block: &domain.ContentBlock{
			Type:      domain.ContentBlockToolUse,
			ToolUseID: call.ID,
			ToolName:  call.LLMName,
			Input:     validated,
		},
	})

	out, isErr, _ := e.runForcedIOCall(ctx, *contract, validated)

	e.appendMessage(domain.ChatMessage{
		ID:   ctx.NewID().String(),
		Role: "tool",
		Content: []domain.ContentBlock{{
			Type:      domain.ContentBlockToolResult,
			ToolUseID: call.ID,
			Text:      out,
			IsError:   isErr,
		}},
	})

	toolStep.Output = out
	toolStep.Text = out
	if isErr {
		toolStep = failStep(toolStep, out)
	} else {
		toolStep.State = "completed"
	}
	e.upsertStep(toolStep)
	e.actionCount++
	e.emitStepEvent(domain.StepEvent{
		Kind:   "block.appended",
		StepID: toolStep.ID,
		TurnID: turnID,
		Block: &domain.ContentBlock{
			Type:      domain.ContentBlockToolResult,
			ToolUseID: call.ID,
			Text:      out,
			IsError:   isErr,
		},
	})
	e.emitStepEvent(domain.StepEvent{
		Kind:   "step.closed",
		StepID: toolStep.ID,
		TurnID: turnID,
	})

	if isErr {
		err := fmt.Errorf("forced IO toolcall %s failed: %s", contract.CallableID, truncate(out, 512))
		e.reportDiagnosticIfSet(ctx, "forced_io", err.Error(), turnID)
		return fmt.Errorf("forced IO dispatch for card %q: %w", contract.CardID, err)
	}

	// 落卡：task_outputs 写 result、状态 doing→done。callable 已经执行，
	// 记账失败也按回合失败上报（可见、可重试）；重试有重复执行副作用的风险，
	// 由 ExpectedStatus=doing 的 CAS 部分缓解（卡若已被移动则落卡直接失败）。
	if e.onForcedIOComplete != nil {
		if cerr := e.onForcedIOComplete(ctx, contract.CardID, out); cerr != nil {
			err := fmt.Errorf("forced IO completion for card %q failed (callable %s already executed): %w", contract.CardID, contract.CallableID, cerr)
			e.reportDiagnosticIfSet(ctx, "forced_io", err.Error(), turnID)
			return err
		}
	}

	e.logger.Info("turnEngine: forced IO dispatch completed",
		"turnID", turnID, "card", contract.CardID, "callable", contract.CallableID)
	e.setLoopState(ctx, turnID, LoopCompleted)
	return nil
}

// runForcedIOCall 执行强制 IO 的 callable。路由与正常工具面一致：
// ServiceName 命中暴露服务则路由过去，否则回落 ctx.Self()（agent-local）。
func (e *turnEngine) runForcedIOCall(ctx actor.Context, contract forcedIOContract, inputJSON string) (string, bool, any) {
	planner := ctx.Planner()
	if planner == nil {
		return "planner not available", true, nil
	}
	svcRef, ok := ctx.LookupService(contract.ToolSpec.ServiceName)
	if !ok {
		svcRef = ctx.Self()
	}
	return callTool(ctx.Lifecycle(), planner, svcRef, contract.CallableID, inputJSON)
}

// --- 纯逻辑（无副作用，单测覆盖）---------------------------------------------

// forcedIOToolChoiceJSON 构造 ToolChoice 强制串。llmclient 三家 provider
// （anthropic/openai/responses）都接受 {"type":"function","function":{"name":…}}
// 的 JSON 对象形式。
func forcedIOToolChoiceJSON(llmName string) string {
	return fmt.Sprintf(`{"type":"function","function":{"name":%q}}`, llmName)
}

// applyForcedIOOverrides 把一次 dispatch 请求改造成强制 IO 往返：工具面
// 收敛为声明的 callable 单工具，ToolChoice 钉死其 LLM 名。
func applyForcedIOOverrides(req *domain.SendSessionMessageReq, spec domain.ToolSpec) {
	req.Tools = []domain.ToolSpec{spec}
	req.ToolChoice = forcedIOToolChoiceJSON(spec.Name)
}

// forcedIOPendingCall 校验强制往返的产物：恰好一个调用、名字与契约工具
// 匹配（复用正常工具面的别名规则：声明名/点号/下划线/唯一裸名，大小写
// 不敏感）。多调用、零调用或名字不符都视为"LLM 未产出合法参数"。
func forcedIOPendingCall(contract forcedIOContract, pending []pendingToolCall) (pendingToolCall, error) {
	if len(pending) == 0 {
		return pendingToolCall{}, errors.New("forced dispatch produced no tool call (provider ignored forced ToolChoice)")
	}
	if len(pending) > 1 {
		return pendingToolCall{}, fmt.Errorf("forced dispatch produced %d tool calls, want exactly 1", len(pending))
	}
	call := pending[0]
	if call.MalformedArgs {
		return pendingToolCall{}, errors.New("forced dispatch tool arguments malformed beyond repair")
	}
	if !forcedIONameMatches(contract.ToolSpec, call.LLMName) {
		return pendingToolCall{}, fmt.Errorf("forced dispatch called %q, want %q", call.LLMName, contract.ToolSpec.Name)
	}
	return call, nil
}

// forcedIONameMatches 报告 LLM 名是否是契约工具的任一别名拼写。
func forcedIONameMatches(spec domain.ToolSpec, llmName string) bool {
	// 单工具集合内裸名必然唯一，直接构造 bareCounts={bare:1} 复用别名规则。
	counts := map[string]int{strings.ToLower(toolBareName(spec.CallableID)): 1}
	want := strings.ToLower(llmName)
	for _, k := range toolAliasKeys(spec, counts) {
		if k == want {
			return true
		}
	}
	return false
}

// validateForcedIOInput 校验并归一 LLM 生成的入参：必须是 JSON object；
// layout 可解析时先 Normalize（卡片/字符串标量按布局族强制转换）再
// Validate，返回归一化后的 JSON 串；layout 为 nil（callable 无
// ReqSchemaID）时仅要求是合法 JSON object，原样透传。
func validateForcedIOInput(layout *protocol.RequestLayout, raw string) (string, error) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return "", fmt.Errorf("arguments are not a JSON object: %w", err)
	}
	if layout == nil {
		return raw, nil
	}
	normalized, errs := layout.Normalize(payload)
	if len(errs) > 0 {
		return "", fmt.Errorf("arguments failed schema validation: %s", joinForcedIOValidationErrors(errs))
	}
	if verrs := layout.Validate(normalized); len(verrs) > 0 {
		return "", fmt.Errorf("arguments failed schema validation: %s", joinForcedIOValidationErrors(verrs))
	}
	out, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("arguments cannot be re-encoded: %w", err)
	}
	return string(out), nil
}

// joinForcedIOValidationErrors 渲染布局校验错误，确定性排序。
func joinForcedIOValidationErrors(errs []error) string {
	msgs := make([]string, 0, len(errs))
	for _, err := range errs {
		msgs = append(msgs, err.Error())
	}
	sort.Strings(msgs)
	return strings.Join(msgs, "; ")
}

// forcedIOInstructionText 生成注入 history 的参数生成指令（用户消息）。
func forcedIOInstructionText(contract forcedIOContract) string {
	var sb strings.Builder
	sb.WriteString("[Turn-end IO contract] Task card \"")
	sb.WriteString(contract.CardID)
	sb.WriteString("\" requires a final tool call before this turn ends: call tool \"")
	sb.WriteString(contract.ToolSpec.Name)
	sb.WriteString("\" (callable \"")
	sb.WriteString(contract.CallableID)
	sb.WriteString("\") with arguments that satisfy its schema. This call is forced; your only job now is to produce the arguments.")
	if contract.Description != "" {
		sb.WriteString("\nContract description: ")
		sb.WriteString(contract.Description)
	}
	return sb.String()
}
