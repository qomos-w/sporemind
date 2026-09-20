package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/protocol"
)

// 回合终局强制 IO 契约的 agent 层解析与落卡（引擎侧见
// turn_engine_forced_io.go）。两条路径都跑在 agent_exec 专用 loop 上
// （phaseForcedDispatch / finalizeDispatch 调用），跨 actor Await 合规。

// resolveForcedIOContract 查询当前 agent 名下 status=doing 且声明 data.io
// 的任务卡，解析出强制 IO 契约。返回 (nil, nil) 表示没有契约（无 project
// 或无匹配卡）；声明了契约但无法解析（callable 不在拓扑注册表 / streaming
// callable）返回错误——这是卡数据错误，必须显式失败而不是静默跳过。
//
// 命中多张时取响应顺序第一张（列表默认按修改时间倒序），每回合只强制
// 一次 toolcall。
func (a *Actor) resolveForcedIOContract(ctx actor.Context) (*forcedIOContract, error) {
	projectRef, ok := ctx.LookupService("project")
	planner := ctx.Planner()
	if !ok || planner == nil {
		// 无 project（standalone agent）= 无任务卡 = 无契约。
		return nil, nil
	}
	listCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	result, err := planner.Call(listCtx, projectRef, "project.wiki_list_cards", domain.WikiListCardsReq{
		Flat:   true,
		Type:   "task",
		Status: "doing",
		// IncludeRaw is required: the metadata-only contract strips Data down
		// to {description, visual, mount-rule keys}, dropping the
		// ownerAgentId/io keys this filter depends on.
		IncludeRaw: true,
		Limit:      -1,
	}).Await()
	if err != nil {
		return nil, fmt.Errorf("project.wiki_list_cards: %w", err)
	}
	var response domain.WikiListCardsResp
	if !decodePromptResult(result, &response) {
		return nil, fmt.Errorf("project.wiki_list_cards: undecodable response")
	}

	callables := a.callablesMap()
	for _, item := range response.Cards {
		callableID, description, applicable := forcedIODeclaration(item.Data, a.actorID)
		if !applicable {
			continue
		}
		ci, known := callables[callableID]
		if !known {
			return nil, fmt.Errorf("task card %q declares data.io.callable %q which is not in the topology registry", item.ID, callableID)
		}
		if ci.Stream {
			return nil, fmt.Errorf("task card %q declares data.io.callable %q which is streaming; forced IO dispatch supports unary callables only", item.ID, callableID)
		}
		specs := ToolSpecsFromCallables(callables, []string{callableID})
		if len(specs) == 0 {
			return nil, fmt.Errorf("task card %q: callable %q produced no tool spec", item.ID, callableID)
		}
		// 入参布局从协议注册表解析（深度投影同工具面）；无 ReqSchemaID 的
		// callable 解析失败 → nil layout，引擎侧跳过布局校验只查 JSON object。
		layout, lerr := protocol.ResolveRequestLayout(ci)
		if lerr != nil {
			layout = nil
		}
		return &forcedIOContract{
			CardID:      item.ID,
			CallableID:  callableID,
			Description: description,
			ToolSpec:    specs[0],
			Layout:      layout,
		}, nil
	}
	return nil, nil
}

// forcedIODeclaration 从列表项已解码的 Data 里提取 IO 契约声明。纯函数：
// 仅当卡 ownerAgentId 等于本 agent 且 data.io.callable 非空时 applicable。
func forcedIODeclaration(data map[string]any, agentID string) (callableID, description string, applicable bool) {
	if data == nil {
		return "", "", false
	}
	owner, _ := data["ownerAgentId"].(string)
	if strings.TrimSpace(owner) != agentID {
		return "", "", false
	}
	ioBlock, ok := data["io"].(map[string]any)
	if !ok {
		return "", "", false
	}
	callableID, _ = ioBlock["callable"].(string)
	callableID = strings.TrimSpace(callableID)
	if callableID == "" {
		return "", "", false
	}
	description, _ = ioBlock["description"].(string)
	return callableID, description, true
}

// completeForcedIO 在强制 toolcall 成功执行后落卡：task_outputs 写
// {"result": …}，状态 doing→done（ExpectedStatus CAS：卡若已被移动则
// 显式失败而不是覆盖他人状态）。先写 outputs 再写 status，保证 done 的
// 卡一定带 result。
func (a *Actor) completeForcedIO(ctx actor.Context, cardID, resultJSON string) error {
	projectRef, ok := ctx.LookupService("project")
	if !ok {
		return fmt.Errorf("project service unavailable")
	}
	planner := ctx.Planner()
	if planner == nil {
		return fmt.Errorf("planner not available")
	}

	outputs := map[string]any{"result": decodeForcedIOResult(resultJSON)}
	outputsCtx, cancelOutputs := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancelOutputs()
	if _, err := planner.Call(outputsCtx, projectRef, "project.wiki_set_task_outputs", domain.WikiSetTaskOutputsReq{
		CardID:  cardID,
		Outputs: outputs,
	}).Await(); err != nil {
		return fmt.Errorf("project.wiki_set_task_outputs: %w", err)
	}

	statusCtx, cancelStatus := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancelStatus()
	if _, err := planner.Call(statusCtx, projectRef, "project.wiki_set_status", domain.WikiSetStatusReq{
		ID:             cardID,
		Status:         "done",
		ExpectedStatus: "doing",
	}).Await(); err != nil {
		return fmt.Errorf("project.wiki_set_status(doing→done): %w", err)
	}
	return nil
}

// decodeForcedIOResult 把 toolcall 结果文本归一化为可写入 task_outputs 的
// 值：JSON object/array 解码为 map/slice，其余原样字符串（与 workspace
// toolcall executor 的 normalizeToolCallResult 同语义）。
func decodeForcedIOResult(resultJSON string) any {
	trimmed := strings.TrimSpace(resultJSON)
	if trimmed == "" {
		return ""
	}
	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return resultJSON
	}
	return decoded
}
