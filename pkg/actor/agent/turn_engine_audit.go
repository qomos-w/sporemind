package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/util"
)

// pendingToolCall 保存一个待执行的工具调用。
// ID / LLMName 来自 LLM 输出；CallableID / ServiceName 用于 actor Plan 调用。
type pendingToolCall struct {
	ID            string
	LLMName       string
	CallableID    string
	UnknownTool   bool
	MalformedArgs bool
	Input         string
	EffectKind    domain.EffectKind
	ServiceName   string
	RawToolCall   string
	// ForkAsync marks a fork call with Async=true: the spawn returns
	// immediately and the result is harvested later via agent_wait.
	ForkAsync bool
}

// toolExecutionBatch 是一组共享同一个权限决策的工具调用。
// 只读调用可以被分组到一个 batch 里并行执行；
// 可变调用单独成一个 batch，确保每次变更都经过用户确认。
type toolExecutionBatch struct {
	calls []pendingToolCall
}

// toolExecutionResult 是单个工具调用的结果。
type toolExecutionResult struct {
	call        pendingToolCall
	out         string
	isErr       bool
	fileChanges []domain.TurnFileChange
	// observation carries an image-bearing user-role message (e.g. a
	// computeruse screenshot) to be appended right after the tool_result so
	// the image rides the same embedding path as user-submitted chat images.
	observation *domain.ChatMessage
	// observationMeta labels the observation step events for the timeline
	// (screenshot / mcp_image); empty falls back to "screenshot".
	observationMeta string
}

// phaseAudit 对 pendingCalls 进行分区，为当前 batch 创建 step 条目，
// 然后迁移到 Execute 让引擎把该 batch 执行到完成。
//
// 流程图：
//
//	┌─────────────────────────┐
//	│  e.batches == nil ?      │
//	│  是 → partitionToolCalls │ 把 pendingCalls 分成若干 batch
//	└───────────┬─────────────┘
//	            ↓
//	┌─────────────────────────┐  所有 batch 已执行完
//	│   e.batchIdx >= len?    │────────→ 清理状态 ──→ LoopDispatch
//	└───────────┬─────────────┘
//	            ↓ 还有 batch
//	┌─────────────────────────┐
//	│  为当前 batch 每个 call   │
//	│  创建 TurnActionToolCall  │
//	│  upsertStep + step.opened │
//	└───────────┬─────────────┘
//	            ↓
//	┌─────────────────────────┐
//	│      LoopExecute        │
//	└─────────────────────────┘
func (e *turnEngine) phaseAudit(ctx actor.Context, turnID string) error {
	if e.batches == nil {
		// 第一次进入 Audit：对 pendingCalls 做分区。
		e.batches = partitionToolCalls(e.pendingCalls)
		e.batchIdx = 0
	}

	if e.batchIdx >= len(e.batches) {
		// 所有 batch 执行完毕，清理状态并回到 Dispatch 开始新一轮 LLM。
		e.pendingCalls = nil
		e.batches = nil
		e.batchIdx = 0
		e.batchSteps = nil
		e.setLoopState(ctx, turnID, LoopDispatch)
		return nil
	}

	batch := e.batches[e.batchIdx]
	batchSteps := make([]domain.TurnAction, len(batch.calls))
	for i, call := range batch.calls {
		toolCallStep := newStep(&e.stepSeq, turnID, domain.TurnActionToolCall, call.LLMName)
		toolCallStep.CallableID = call.CallableID
		toolCallStep.TargetService = call.ServiceName
		toolCallStep.Text = call.Input
		toolCallStep.Input = call.Input
		toolCallStep.ToolUseID = call.ID
		toolCallStep.Meta = call.ID
		batchSteps[i] = toolCallStep
		e.upsertStep(toolCallStep)
		e.emitStepEvent(domain.StepEvent{
			Kind:     "step.opened",
			StepID:   toolCallStep.ID,
			TurnID:   turnID,
			StepType: "tool_call",
			Role:     "assistant",
			Block: &domain.ContentBlock{
				Type:      domain.ContentBlockToolUse,
				ToolUseID: call.ID,
				ToolName:  call.LLMName,
				Input:     call.Input,
			},
		})
	}
	e.batchSteps = batchSteps
	e.setLoopState(ctx, turnID, LoopExecute)
	return nil
}

// partitionToolCalls 把连续只读调用合并到一个 batch，
// 并把可变调用隔离到各自的 batch 中。
//
// 分区规则：
//   - 只读调用（EffectNone）连续出现时合并；
//   - 可变调用（EffectRead / EffectWrite / EffectIrreversible）单独成 batch；
//   - 可变调用后面的只读调用也会开始新 batch，因为 currentEffect != EffectNone。
func partitionToolCalls(calls []pendingToolCall) []toolExecutionBatch {
	if len(calls) == 0 {
		return nil
	}
	batches := make([]toolExecutionBatch, 0, len(calls))
	current := toolExecutionBatch{}
	currentEffect := calls[0].EffectKind
	if currentEffect == "" {
		currentEffect = domain.EffectNone
	}
	for _, call := range calls {
		effect := call.EffectKind
		if effect == "" {
			effect = domain.EffectNone
			call.EffectKind = effect
		}
		if len(current.calls) == 0 {
			current.calls = append(current.calls, call)
			currentEffect = effect
			continue
		}
		// 可变调用永远拆 batch；
		// 只读调用如果前面有可变调用也要拆，因为 currentEffect 不是 None。
		if effect != domain.EffectNone || currentEffect != domain.EffectNone {
			batches = append(batches, current)
			current = toolExecutionBatch{calls: []pendingToolCall{call}}
			currentEffect = effect
			continue
		}
		current.calls = append(current.calls, call)
	}
	if len(current.calls) > 0 {
		batches = append(batches, current)
	}
	return batches
}

// isPathInsideRoots reports whether p lies inside at least one of the roots.
// Relative paths are resolved against the first root. Paths and roots are
// normalized to forward slashes for cross-platform checks.
func isPathInsideRoots(p string, roots []string) bool {
	if len(roots) == 0 {
		return true
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(roots[0], p)
	}
	cp := util.NormalizePath(p)
	for _, root := range roots {
		if cp == root || strings.HasPrefix(cp, root+"/") {
			return true
		}
	}
	return false
}

// resolveToolPath resolves the filesystem target path for a file mutating tool.
// It returns the normalized path and true when the call carries a path.
func resolveToolPath(call pendingToolCall) (string, bool) {
	switch call.CallableID {
	case "filesystem.write", "filesystem.edit", "filesystem.rm",
		"project.write", "project.edit", "project.rm":
		var req struct {
			Path string `json:"Path"`
		}
		if err := json.Unmarshal([]byte(call.Input), &req); err != nil {
			return "", false
		}
		if req.Path == "" {
			return "", false
		}
		return util.NormalizePath(req.Path), true
	}
	return "", false
}

// bypassRiskyPatterns 命中 callable ID 即视为风险操作，在 bypass 行为
// （auto/autopilot）下必须交 fast model 逐案裁决：命令执行、删除/移除、
// agent 终止、网络推送与远端改写、物理交互、凭据与秘密访问。
var bypassRiskyPatterns = []string{
	"shell_exec", ".exec", ".rm", "delete", "remove", "terminate", "unregister",
	"git_push", "git_reset", "git_clean", "interact",
	"authenticator", "credential", "secret", "apikey",
}

// bypassBatchNeedsReview 报告 bypass 行为下的 batch 是否属于风险类操作：
// 任一调用命中风险模式，或其可解析目标路径落在所有已配置根目录之外。
// 常规范围内的变更返回 false，由调用方直接放行（不产生 LLM 往返）。
func bypassBatchNeedsReview(e *turnEngine, batch toolExecutionBatch) bool {
	for _, call := range batch.calls {
		id := strings.ToLower(call.CallableID)
		for _, p := range bypassRiskyPatterns {
			if strings.Contains(id, p) {
				return true
			}
		}
		if len(e.roots) > 0 {
			if path, ok := resolveToolPath(call); ok && !isPathInsideRoots(path, e.roots) {
				return true
			}
		}
	}
	return false
}

// registrationCallableIDs lists the callables that install or register an app
// into the host. They extend the trust boundary with new executable code, so
// the turn engine forces them through the interactive permission step in
// every permission mode — yolo/allow-all/auto/autopilot auto-approval,
// EffectNone classification, project-level pre-authorization, and the
// AutoAllowTools whitelist are all overridden. Mirrored by
// web/src/ui/ai/model/app-registration.ts (APP_REGISTRATION_CALLABLES).
var registrationCallableIDs = map[string]struct{}{
	"appmanager.register":          {},
	"appmanager.register_project":  {},
	"appmanager.install_local":     {},
	"cloudaccount.content_install": {},
}

func isRegistrationCallable(callableID string) bool {
	_, ok := registrationCallableIDs[callableID]
	return ok
}

// registrationPreviewTimeout bounds the appmanager.registration_preview call
// the engine makes while enriching a permission event. The permission panel
// must not be delayed indefinitely by a stuck preview; the user waits for the
// panel to appear before anything else happens.
const registrationPreviewTimeout = 5 * time.Second

// previewReqFromToolInput extracts the registration-preview source fields from
// a tool call's input JSON. Only the source fields are decoded; large payloads
// (register's Modules map) are skipped by the selective struct.
func previewReqFromToolInput(callableID, input string) gen.AppManagerRegistrationPreviewReq {
	var parsed struct {
		Manifest  *gen.AppManifest `json:"Manifest"`
		ProjectID string           `json:"ProjectId"`
		AppID     string           `json:"AppId"`
		AppDir    string           `json:"AppDir"`
		Path      string           `json:"Path"`
		Slug      string           `json:"Slug"`
	}
	if input != "" {
		_ = json.Unmarshal([]byte(input), &parsed)
	}
	req := gen.AppManagerRegistrationPreviewReq{
		Manifest:  parsed.Manifest,
		ProjectID: parsed.ProjectID,
		AppID:     parsed.AppID,
		AppDir:    parsed.AppDir,
		Path:      parsed.Path,
		Slug:      parsed.Slug,
	}
	// Sources the tool input cannot supply authoritatively: the bound project
	// comes from the engine's agent identity (the LLM could otherwise point the
	// preview at an unrelated project).
	if callableID == "appmanager.register_project" {
		req.Slug = ""
		req.Path = ""
	}
	if callableID == "appmanager.install_local" {
		req.Slug = ""
	}
	if callableID == "cloudaccount.content_install" {
		req.Path = ""
	}
	return req
}

// enrichRegistrationSummaries attaches the host permissions each intercepted
// registration call would grant (resolved via appmanager.registration_preview)
// to the permission summaries, so the panel shows 宿主权限 before approval.
// Best-effort and bounded: on failure the summary carries a short
// PermissionNote and the panel shows the unavailable warning instead of
// silently hiding the permission surface.
func (e *turnEngine) enrichRegistrationSummaries(ctx actor.Context, summaries []domain.ToolCallSummary) {
	hasRegistration := false
	for i := range summaries {
		if isRegistrationCallable(summaries[i].CallableID) {
			hasRegistration = true
			break
		}
	}
	if !hasRegistration {
		return
	}
	planner := ctx.Planner()
	appRef, ok := ctx.LookupService("appmanager")
	if planner == nil || !ok || appRef == nil {
		for i := range summaries {
			if isRegistrationCallable(summaries[i].CallableID) {
				summaries[i].PermissionNote = "appmanager service unavailable"
			}
		}
		return
	}
	for i := range summaries {
		if !isRegistrationCallable(summaries[i].CallableID) {
			continue
		}
		req := previewReqFromToolInput(summaries[i].CallableID, summaries[i].Input)
		// The engine injects CallerAgentId only at execution time, after this
		// gate; supply it here so an omitted ProjectId resolves to the calling
		// agent's bound project exactly as the executed call will.
		req.CallerAgentID = e.agentID
		callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), registrationPreviewTimeout)
		value, err := planner.Call(callCtx, appRef, "appmanager.registration_preview", req).Await()
		cancel()
		if err != nil {
			summaries[i].PermissionNote = truncatePreviewNote(err.Error())
			continue
		}
		resp, ok := value.(gen.AppManagerRegistrationPreviewResp)
		if !ok || resp.Manifest.ID == "" {
			summaries[i].PermissionNote = "unexpected registration preview response"
			continue
		}
		summaries[i].AppID = resp.Manifest.ID
		summaries[i].AppName = resp.Manifest.Name
		summaries[i].Permissions = resp.Manifest.Permissions
	}
}

// truncatePreviewNote keeps preview failure notes short in the interaction
// payload (they may carry upstream error strings).
func truncatePreviewNote(s string) string {
	if len(s) > 200 {
		return s[:200] + "...(truncated)"
	}
	return s
}

// isInterceptedInteractionCallable reports whether the callable is an
// agent-owned interaction primitive that the turn engine handles internally
// (goal/plan/turn control) rather than dispatching as a card-provided tool.
// These are always available when their mode/bundle is mounted and are exempt
// from the card-scope permission gate; gating them breaks goal/plan completion
// whenever the contributing card is mounted with a scope outside the allowlist.
func isInterceptedInteractionCallable(callableID string) bool {
	switch callableID {
	case "turn_assess", "goal_submit", "goal_card_submit", "plan_submit", "workflow_start":
		return true
	}
	return false
}

// callableScopeAllowed reports whether a card-provided callable may run under
// the card-scope permission gate. Callables not contributed by any card are
// unrestricted. Trusted mount scopes are builtin/project/user/dependency/app:
// "app" is stamped only by agent_bind_app (internal, reachable solely via the
// workspace's admin-gated create_app_agent), so a bound app's manifest
// declaration is itself the authorization. "system" is tolerated for legacy
// persisted mounts only: syncWorktreeModeCard stamped it on builtin:mode:worktree
// before it moved to "builtin"; agents restored from that era still carry
// scope "system" in their persisted mounts.
func callableScopeAllowed(callableScopes map[string]string, callableID string) bool {
	scope, ok := callableScopes[callableID]
	if !ok {
		return true
	}
	switch scope {
	case "builtin", "project", "user", "dependency", "app", "system":
		return true
	}
	return false
}

// checkBatchPermission 判断当前 batch 是否需要用户确认。
// 返回决策字符串："allow" / "deny" / "confirm" / "bypass"，以及原因说明。
func (e *turnEngine) checkBatchPermission(ctx actor.Context, batch toolExecutionBatch) (string, string) {
	if e.allowCallableScope != nil {
		for _, call := range batch.calls {
			// Internally-intercepted interaction primitives (turn.assess,
			// goal.submit, plan.submit) are not card-provided security tools and
			// must bypass the card-scope check. See isInterceptedInteractionCallable.
			if isInterceptedInteractionCallable(call.CallableID) {
				continue
			}
			if !e.allowCallableScope(call.CallableID) {
				return "deny", fmt.Sprintf("callable %q is not enabled by an allowed card scope", call.CallableID)
			}
		}
	}
	// 注册类 callable 一律强制交互确认：向宿主注入新可执行代码会扩大信任
	// 边界，任何 permission mode、EffectKind 分类、项目级预授权或白名单都
	// 不得静默放行（注册必须弹权限确认框，不得静默注册）。
	for _, call := range batch.calls {
		if isRegistrationCallable(call.CallableID) {
			return "confirm", "app registration requires explicit user authorization"
		}
	}

	// 只读 batch 直接放行。
	if batch.calls[0].EffectKind == domain.EffectNone {
		return "allow", ""
	}

	// 检查项目级已授权工具。
	if e.hasProjectApproval != nil && e.projectID != "" {
		allApproved := true
		for _, call := range batch.calls {
			if !e.hasProjectApproval(e.projectID, call.CallableID) {
				allApproved = false
				break
			}
		}
		if allApproved {
			return "allow", "project-approved"
		}
	}
	policy := e.startReq.CompiledContext.PermissionPolicy
	// 实时读取 agent 当前权限模式，覆盖 turn 启动时烘焙的 DefaultBehavior 快照。
	// 这样用户在 turn 运行中切换 yolo/bypass/permission 能立即生效。
	defaultBehavior := ""
	if policy != nil {
		defaultBehavior = policy.DefaultBehavior
	}
	if e.livePermissionMode != nil {
		switch e.livePermissionMode() {
		case "yolo", "allow-all":
			defaultBehavior = "allow"
		case "auto", "autopilot":
			defaultBehavior = "bypass"
		case "", "permission":
			if defaultBehavior == "" || (defaultBehavior != "allow" && defaultBehavior != "bypass") {
				defaultBehavior = "confirm"
			}
		}
	}
	if policy != nil {
		// AutoAllowTools 白名单中的工具无需确认。
		for _, call := range batch.calls {
			for _, allowed := range policy.AutoAllowTools {
				if call.CallableID == allowed {
					return "allow", "auto-allowed"
				}
			}
		}
	}
	// DefaultBehavior 为 allow（yolo）时默认放行所有工具，包括落在已配置根目录
	// 之外的路径：yolo 模式的语义就是自动批准一切。执行层会在真正调用工具前为
	// 文件变更类工具补上 confirm=true，避免 actor 层把它们当成越权硬报错。
	if defaultBehavior == "allow" {
		return "allow", "default-allow"
	}
	// bypass 模式（auto/autopilot）：守卫目标是危险与攻击行为，不是所有可变
	// 操作。范围内的常规变更（文件编辑、分支、wiki 写入等）直接放行，省去
	// 一次 fast model 往返；仅风险类操作（命令执行、删除、网络推送、凭据
	// 访问、roots 外路径）交由 fast model 单次非上下文调用逐案裁决。
	if defaultBehavior == "bypass" {
		if bypassBatchNeedsReview(e, batch) {
			return "bypass", "bypass-mode"
		}
		return "allow", "bypass-routine"
	}

	// 到这里说明既非自动放行（yolo/bypass）也非项目级已授权。若可变文件操作的目标
	// 路径落在所有已配置根目录之外，需要用户显式确认（走审批），而不是直接报错。
	roots := e.roots
	if len(roots) > 0 {
		for _, call := range batch.calls {
			p, ok := resolveToolPath(call)
			if !ok {
				continue
			}
			if !isPathInsideRoots(p, roots) {
				return "confirm", fmt.Sprintf("path %q is outside all configured roots", p)
			}
		}
	}

	// 其余可变调用需要用户确认。
	return "confirm", "mutating tool requires confirmation"
}

// toolEffectIndex 构建 tool name → EffectKind 的映射。键集与 buildToolIndex
// 完全一致（声明名、点号 callable ID、下划线形式，以及唯一时的裸名），保证
// 无论 LLM 用哪种拼写，效果分类都能命中。所有 key 均转小写。
func toolEffectIndex(tools []domain.ToolSpec) map[string]domain.EffectKind {
	counts := bareNameCounts(tools)
	out := make(map[string]domain.EffectKind, len(tools)*4)
	for _, tool := range tools {
		effect := domain.EffectKind(tool.EffectKind)
		if effect == "" {
			effect = domain.EffectNone
		}
		for _, k := range toolAliasKeys(tool, counts) {
			out[k] = effect
		}
	}
	return out
}

// askUserToolSpec 返回内置 ask_user 工具的规格。
// 仅在非 yolo 模式下加入可用工具列表（见 rebuildToolsForModel），让 LLM
// 可以在必要时向用户提问；yolo 模式下不注入，agent 不得向用户提问。
func askUserToolSpec() domain.ToolSpec {
	return domain.ToolSpec{
		Name:        "ask_user",
		Description: "Ask the user one or more questions and wait for answers before continuing. Each option may set \"recommended\": true to mark the best choice (at most one per question); the UI highlights recommended options. The tool result is a JSON object encoded as text, with question indices as keys and selected option labels as values; for example, {\"0\":\"Option A\"}.",
		InputSchema: `{"type":"object","properties":{"questions":{"type":"array","items":{"type":"object","properties":{"header":{"type":"string"},"question":{"type":"string"},"options":{"type":"array","items":{"type":"object","properties":{"label":{"type":"string"},"description":{"type":"string"},"recommended":{"type":"boolean"}}}},"multiSelect":{"type":"boolean"}},"required":["header","question","options","multiSelect"]}}},"required":["questions"]}`,
		CallableID:  "ask_user",
		ServiceName: "",
		EffectKind:  string(domain.EffectIrreversible),
	}
}

// ---- Step 辅助函数 ----

// newStep 用单调 seq 创建一个新的 TurnAction，状态默认为 running。
func newStep(seq *int, turnID string, kind domain.TurnActionKind, title string) domain.TurnAction {
	*seq++
	return newStepPtr(*seq, turnID, kind, title)
}

func newStepPtr(seq int, turnID string, kind domain.TurnActionKind, title string) domain.TurnAction {
	return domain.TurnAction{
		ID:    fmt.Sprintf("%s-%s-%03d", turnID, stepIDPrefix(kind), seq),
		Kind:  string(kind),
		Title: title,
		State: "running",
		Seq:   int64(seq),
	}
}

// initialStepSeqForTurn returns the highest step sequence number embedded in
// existing step IDs for the given turn, so a resumed turn's new engine starts
// its stepSeq above all prior steps. Without this, a crash-recovery resume
// reuses the same turn ID but resets stepSeq to 0, producing step IDs that
// collide with the crashed turn's steps. The frontend's step.opened idempotency
// check (step-reducer.ts) then silently ignores the new step, and subsequent
// block.delta events either append to stale content or get lost.
func initialStepSeqForTurn(steps []domain.Step, turnID string) int {
	maxSeq := 0
	prefix := turnID + "-"
	for _, s := range steps {
		if s.TurnID != turnID {
			continue
		}
		id := s.ID
		if !strings.HasPrefix(id, prefix) {
			continue
		}
		rest := id[len(prefix):]
		dashIdx := strings.LastIndex(rest, "-")
		if dashIdx < 0 {
			continue
		}
		n := 0
		for _, c := range rest[dashIdx+1:] {
			if c < '0' || c > '9' {
				n = 0
				break
			}
			n = n*10 + int(c-'0')
		}
		if n > maxSeq {
			maxSeq = n
		}
	}
	return maxSeq
}

// stepIDPrefix 根据 step 类型返回 ID 前缀，便于从 ID 中识别类型。
func stepIDPrefix(kind domain.TurnActionKind) string {
	switch kind {
	case domain.TurnActionLLMCall:
		return "llm"
	case domain.TurnActionReasoning:
		return "reasoning"
	case domain.TurnActionToolCall:
		return "tool"
	case domain.TurnActionError:
		return "err"
	case domain.TurnActionTurnStart:
		return "start"
	default:
		return "step"
	}
}

// failStep 把 step 标记为失败，并记录错误信息。
func failStep(step domain.TurnAction, msg string) domain.TurnAction {
	step.State = "failed"
	step.Error = msg
	return step
}

// upsertStep 把 step 存储到引擎的 step 映射中。
// 如果是首次插入，还会追加到 stepOrder 并设置 StartedAt。
func (e *turnEngine) upsertStep(step domain.TurnAction) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stepByID == nil {
		e.stepByID = make(map[string]domain.TurnAction)
	}
	if _, exists := e.stepByID[step.ID]; !exists {
		e.stepOrder = append(e.stepOrder, step.ID)
		if step.StartedAt == "" {
			step.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
		}
	}
	e.stepByID[step.ID] = step
}
