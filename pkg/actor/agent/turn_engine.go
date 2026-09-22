package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/plan"
	"github.com/qomos-w/sporemind/pkg/compaction"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"

	"github.com/qomos-w/sporemind/pkg/llmclient"
	"github.com/qomos-w/sporemind/pkg/tokenest"
	"github.com/qomos-w/sporemind/pkg/util"
	"strings"
	"sync"
	"time"
)

// childResult 是 turn.childResult 的镜像，让 agent 包可以在不引入 turn
// 内部实现的情况下接收 fork 子 agent 的返回结果。
type childResult struct {
	ToolUseID string
	Result    domain.ForkResult
	Err       error
}

// TurnLoopState 是 turnEngine 内联状态机的显式阶段枚举。
// 整个一次对话轮次（turn）被拆成 4 个活跃阶段 + 3 个终止阶段：
//
//	Dispatch -> Audit -> Execute -> Commit --┐
//	                                       │
//	                                       ↓
//	                               Completed / Failed / Cancelled
//
// 终止阶段不可复活：到达 Completed/Failed/Cancelled 后，任何新输入都必须由
// 新的 assistant TurnID 与不可变的 TurnOrder 处理，旧的 terminal record 保持
// 不变。
type TurnLoopState int32

const (
	LoopDispatch       TurnLoopState = iota // 调用 LLM，解析模型输出
	LoopAudit                               // 对工具调用进行分区、建 step、权限检查
	LoopExecute                             // 执行工具调用批次，处理 ask_user/permission/fork_child 等待
	LoopForcedDispatch                      // 回合终局强制 IO toolcall：ToolChoice 钉死的额外一次 LLM 往返
	LoopPaused                              // 用户暂停：等待恢复信号
	LoopCompleted                           // 正常结束
	LoopFailed                              // 出错结束
	LoopCancelled                           // 被取消
)

// unitSlotRetryBudget is the extra dispatch-retry budget granted when the
// active model slot pins a concrete unit. Such a slot has no aggregator
// fallback candidate, so a transient provider failure can only be recovered by
// retrying the same endpoint — give it more attempts before giving up.
const unitSlotRetryBudget = 5

func (s TurnLoopState) String() string {
	switch s {
	case LoopDispatch:
		return "dispatch"
	case LoopAudit:
		return "audit"
	case LoopExecute:
		return "execute"
	case LoopForcedDispatch:
		return "forced_dispatch"
	case LoopPaused:
		return "paused"
	case LoopCompleted:
		return "completed"
	case LoopFailed:
		return "failed"
	case LoopCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

// IsTerminal reports whether s is a terminal lifecycle state.
func (s TurnLoopState) IsTerminal() bool {
	switch s {
	case LoopCompleted, LoopFailed, LoopCancelled:
		return true
	}
	return false
}

// CanTransition reports whether a direct transition from s to to is allowed.
// Terminal states (Completed/Failed/Cancelled) cannot transition back to any
// active state; the engine must exit and the agent layer creates a successor
// turn with a new TurnID/TurnOrder for any subsequent input.
func (s TurnLoopState) CanTransition(to TurnLoopState) bool {
	if s == to {
		return true
	}
	return !s.IsTerminal()
}

// pendingChild 跟踪一个尚未完成的 fork_child 子 agent。
type pendingChild struct {
	ToolUseID string    // LLM 生成的 tool_call_id
	StepID    string    // 对应 TurnAction step 的 ID，用于 UI 路由
	Kind      string    // 子 agent 的 kind（explorer / reviewer / general 等）
	SpawnedAt time.Time // 用于子 agent 超时判定
	// Async 标记 Async=true 的 fork：step 已立即以 spawn 应答收尾，
	// batch 不为它扣留；结果由 agent_wait 收割（handleChildResult 落入
	// asyncChildResults 而不是改写历史占位符）。
	Async bool
	// AgentID 是 workspace 返回的子 actor ID，agent_wait 按 ID 定向收割。
	AgentID string
}

// asyncChildOutcome 记录一个已完成的 async fork 子 agent 的终态，
// 供 agent_wait 在同一 turn 内晚到收割时读取。
type asyncChildOutcome struct {
	AgentID string
	Status  string // completed | failed
	Result  domain.ForkResult
	Err     string
}

// turnEngine 承载一次 turn 的全部可变状态。
// 它运行在 agent.exec 自定义 actor 循环上，agent 侧处理器（cancel、
// append message 等）直接访问该结构，不跨 actor 边界。
type turnEngine struct {
	startReq    domain.TurnStartReq // 本次 turn 的启动请求
	projectID   string              // agent 所属项目的 actor ID，用于项目级权限授权
	workspaceID string              // parent workspace actor id for aistats telemetry
	agentID     string              // own agent actor id for aistats telemetry

	logger actor.Logger
	// resolveTargets resolves a slot into an ordered list of dispatch
	// candidates (aggregator ref + optional unit). The agent owns slot state.
	resolveTargets func(ctx actor.PureContext, slot domain.ModelSlot) []dispatchTarget
	primarySlot    domain.ModelSlot
	fastSlot       domain.ModelSlot
	summarySlot    domain.ModelSlot

	// turnID 是本次 turn 的规范标识，所有事件都带这个 ID。
	// 如果为空，run() 会生成一个。
	turnID string

	mu         sync.RWMutex
	cancelled  bool
	done       chan struct{} // 取消信号通道
	turnCtx    context.Context
	turnCancel context.CancelFunc

	// runExited 在 run() 返回时关闭。让 handleTurnCancel 能在 default loop
	// 上等 engine 完成所有收尾（writeCancelledToolResults +
	// reconcileOrphanToolUses + closeRemainingOpenSteps）后再快照 a.steps，
	// 避免把孤儿 tool_use 落进持久化的 cancelled turn。
	runExited chan struct{}

	// loopState 当前所处状态机阶段。
	// 只由 run() 在 agent.exec 循环上写；snapshot / inspect 只读。
	loopState TurnLoopState

	// turnError 记录导致 turn 进入 LoopFailed 的错误信息，
	// 用于 getDelta() 填充 Turn.Error，最终经 turn.failed 事件回流到前端。
	turnError string

	// compactionFailures counts consecutive compaction failures within this
	// turn. Drives the circuit breaker in shouldCompactAfterDispatch so a
	// failing summarizer stops being retried. Written only from run() on the
	// agent.exec loop; no lock needed (same single-writer model as loopState).
	compactionFailures int

	// dispatchResolvedUnit is the concrete unit the aggregator selected for
	// the CURRENT dispatch attempt (from the resolved_unit chunk; empty until
	// reported or when the stream never opened). Cleared at each runDispatch
	// entry so a pre-open failure is never attributed to the previous
	// attempt's unit. Same single-writer model as loopState: written on the
	// agent.exec loop inside runDispatch's chunk routing.
	dispatchResolvedUnit domain.ModelUnit

	// acceptingInjects 为 true 时表示本 turn 还能把用户/peer 消息注入当前
	// history（例如 tool 执行间隙的实时追加）。finalizeDispatch 决定走
	// LoopCompleted 路径时将其置为 false，避免消息在 turn 收尾阶段被吞掉。
	acceptingInjects bool

	// taskReminderEmitted prevents the unfinished-task reminder from firing
	// more than once per turn, avoiding an infinite loop where the LLM keeps
	// producing text completions while tasks remain unfinished.
	taskReminderEmitted bool

	// loopGuardRing/loopGuardStrikes 驱动同 turn 内 dispatch 迭代的
	// 近重复检测（见 turn_engine_loopguard.go）。只在 agent.exec 循环上
	// 读写，与 loopState 相同的单写者模型，无需加锁。
	loopGuardRing    []loopGuardEntry
	loopGuardStrikes int

	// startedAt 记录 turn 开始时间，用于 StartedAt / Elapsed。
	startedAt time.Time

	// stepEvents 缓冲 step 级别事件，最终通过 ctx.EmitEvent 和 onStepEvent 刷出。
	stepEvents []domain.StepEvent

	// openStepEvents 保留每个未关闭 step 的事件序列。断线重连时后端把这些
	// 事件随 summary 返回，前端可以按 EventSeq 重放以恢复仍在进行中的 step。
	// step.closed / step.error 到达时清理对应条目。
	openStepEvents map[string][]domain.StepEvent

	// stepEventSeq 是本次 turn 内单调递增的 step 事件序号。
	// 每个 StepEvent 获得唯一序号，前端用其做 EventSeq 去重和断线重连回放。
	stepEventSeq int32

	// stepEventMu 保护 stepEvents、openStepEvents 和 stepEventSeq，防止 handleExploreProgress
	// (Internal callable, 默认 loop) 与 turn 引擎主循环 (agent.exec loop) 竞态。
	stepEventMu sync.Mutex

	// turnEvents 缓冲 turn 级别元数据事件（context_budget 等）。
	turnEvents []domain.TurnEvent

	// onStepEvent 在 flushEvents 时被调用，把 step 事件应用到 agent 状态。
	onStepEvent func(domain.StepEvent)

	// onEventForward 在每个 step 事件成功广播到事件总线后被调用，
	// 由 agent 层挂接 forwardToPlugins（fire-and-forget 转发到
	// pluginhost.event_deliver，供 listen step 的插件接收）。nil 时跳过。
	onEventForward func(ctx actor.Context, kind string, payload any)

	// onToolResult 在 flushEvents 时对每个落盘的 tool_result block 调用。
	// 它由 agent 层挂接（如 coordinator 的首个工具调用微提示），带 ctx 以便
	// 执行跨 actor 副作用；nil 时被跳过。
	onToolResult func(ctx actor.Context, ev domain.StepEvent)

	// onUsageUpdated 在每次 dispatch 用量累加后被调用，传入累积用量。
	onUsageUpdated func(*domain.UsageData)

	// onContextBudgetUpdated 在每次 dispatch 后被调用，传入完整 usage 数据
	// 以便 agent 更新 ContextBudget（含 piggyback 的 MaxContextLength）并通知前端进度条。
	// 仅当 provider 返回真实 usage 时调用。
	onContextBudgetUpdated func(usage *domain.UsageData)

	// onContextBudgetEstimate 在 provider 完全不返回 usage 时被调用，传入
	// tiktoken 本地估算的输入 token 数。仅用于驱动 budget bar 显示，使上下文
	// 占用可见；不进入 cost/cache/requestStats，不覆盖已收到的真实值。
	onContextBudgetEstimate func(estimated int64)

	// resolveTokenBudget 返回当前 live 的 token budget（windowSize * policy%）。
	// 用于 shouldCompactAfterDispatch，确保触发阈值和预算条显示始终一致。
	// 返回 0 表示不可用，回退到 CompiledContext 冻结值。
	resolveTokenBudget func() int32

	// onCalibrate 在每次 dispatch 后被调用，传入字符计数、估算值和实际值
	// 以便 agent 校正 token 估算系数并持久化。
	onCalibrate func(cc tokenest.CharCounts, estimated, actual int)

	// calibration 是 agent 持有的 token 估算校准对象，用于按增量预估 token。
	// 可能为 nil（无历史校准数据时），此时退化为默认系数。
	calibration *tokenest.Calibration

	// forkKindMap maps LLM-facing fork tool names to child agent kinds, sourced
	// from the parent agent's bundle cards (data.fork declarations). Used by
	// forkKindForLLMName to route a fork tool call to the right child role.
	forkKindMap map[string]string

	// onChildTimeout stops a child that the parent has stopped waiting for.
	onChildTimeout func(ctx actor.Context, toolUseID string)
	// onChildLiveness reads the child-owned atomic activity timestamp through
	// the child's stateless agent_status callable.
	onChildLiveness func(ctx actor.Context, toolUseID string) (time.Time, error)
	// onChildTrack registers a newly spawned fork child in the parent agent's
	// activeChildren map so cancellation, liveness checks, and explore_complete
	// routing work. The workspace is the spawner; the engine only tracks the
	// returned ActorID.
	onChildTrack func(ctx actor.Context, toolUseID, childActorID string)

	// onCompact is called when the engine needs to compact context mid-turn.
	// stepID is an optional placeholder step ID that the engine has already
	// opened; if non-empty, compactAsStep should reuse it so the UI sees a
	// single compaction step transition from "Compressing..." to the result.
	onCompact func(ctx actor.Context, turnID string, stepID string) error

	// onCostRate resolves a cost rate for a provider/model so per-request cost
	// can be recorded in TurnRequestStat.Usage.
	onCostRate func(ctx actor.Context, provider, model string) (llmclient.CostRate, bool)

	// onStatsRecord submits a failed-dispatch record to the aistats actor. It
	// fires on turn-level idle timeouts: when the turn engine's own idle timer
	// trips it cancels the aggregator stream, which the aggregator cannot
	// distinguish from a benign pause and would otherwise drop. The turn
	// engine records the timeout so it shows up in error-code stats.
	onStatsRecord func(ctx actor.Context, record domain.AIStatsRecord)

	// openToolCalls 追踪本 turn 内已开始但未收到 tool_result 的 tool_use 数量。
	// 压缩必须在该计数为 0 时才允许执行，否则会把 tool_use / tool_result 对切
	// 开，导致下一轮 LLM 收到孤儿 tool_result。
	openToolCalls int

	// onToolExecuted 每次工具执行后被调用，传入 callable ID 和 input。
	// 仅对 child-mode agent 设置，用于向父 agent 汇报 explore 进度。
	onToolExecuted func(ctx actor.Context, callableID, input string)

	// onAskUser 在 ask_user 事件发出时调用，让 agent 更新聚合状态。
	onAskUser func()

	// onPermissionRequested 在 permission_requested 事件发出时调用。
	onPermissionRequested func()

	// onUnfinishedTasks returns the agent's unfinished tasks (pending or
	// in_progress). Called by finalizeDispatch when the LLM produces a text-
	// completion stop (no tool calls). If the returned slice is non-empty the
	// engine appends a reminder assistant message and continues the loop
	// instead of completing the turn, so the LLM acts on the reminder.
	onUnfinishedTasks func() []gen.TurnTask

	// onInteractionRequested records the blocking interaction so it survives an
	// agent restart. Called from the ask_user and permission emit points.
	onInteractionRequested func(ctx actor.Context, turnID, stepID, requestID, typ string, task map[string]any)

	// plan mode callbacks
	onPlanSubmit          func(ctx actor.Context, input string) (requestID string, err error)
	onPlanSubmitted       func(ctx actor.Context, turnID, requestID string)
	onPlanResolveApproval func(ctx actor.Context, answer string) (decision string, tasks []gen.Task, requestID string, feedback string)

	// onPlanExpire 在 plan 等待审批时 turn 被取消(<-e.done / lifecycle done)
	// 触发,用于让 agent 把 a.plan.Status 从 pending_approval 降级到 rejected,
	// 防止下次 plan_submit 被 "plan already submitted" 挡住。仅清状态,不发事件、
	// 不 notify(避免与 turn cleanup 最终 snapshot 乱序覆盖)。
	onPlanExpire func(ctx actor.Context, requestID string)

	// goal_submit callbacks
	onGoalSubmit        func(ctx actor.Context, input string) (requestID string, err error)
	onGoalSubmitted     func(ctx actor.Context, turnID, requestID string)
	onGoalResolveSubmit func(ctx actor.Context, answer string) (decision string, requestID string, feedback string)
	onGoalExpire        func(ctx actor.Context, requestID string)
	onGoalBlockRefresh  func(ctx actor.Context) *domain.ContentBlock

	// workflow_start callbacks
	onWorkflowStart              func(ctx actor.Context, input string) (requestID string, err error)
	onWorkflowStartSubmitted     func(ctx actor.Context, turnID, requestID string)
	onWorkflowStartResolveSubmit func(ctx actor.Context, answer string) (decision string, requestID string, feedback string)
	onWorkflowStartExpire        func(ctx actor.Context, requestID string)

	// workflow_plan_submit callback (first phase only; confirmation, resolve,
	// and expire reuse the workflow_start callbacks because the pending state
	// is the same PendingWorkflowStart record set by applyWorkflowStart).
	onWorkflowPlanSubmit func(ctx actor.Context, input string) (requestID string, err error)

	// goal_card_submit callbacks
	onGoalCardSubmit        func(ctx actor.Context, input string) (requestID string, err error)
	onGoalCardSubmitted     func(ctx actor.Context, turnID, requestID string)
	onGoalCardResolveSubmit func(ctx actor.Context, answer string) (decision string, requestID string, feedback string, cardID string, err error)
	onGoalCardExpire        func(ctx actor.Context, requestID string)

	// onGoalCondition returns the active session goal condition text, used to
	// resolve a reviewer child's ReviewText before dispatch when the LLM omits
	// it. Pure read of actor state; nil-safe.
	onGoalCondition func() string

	// assessValidator, when non-nil, vets a turn.assess Decision before it is
	// recorded (e.g. a goal bound to a task card must use ready_for_review,
	// not complete_candidate — completion requires external review).
	assessValidator func(decision string) error

	assessment *gen.TurnAssessment

	// onReportDiagnostic 在错误发生时被调用，向 oracle actor 上报诊断。
	onReportDiagnostic func(ctx actor.Context, req domain.OracleReportDiagnosticReq)

	// saveSnapshot 将大文件内容写入 agent 快照目录，返回 snapshot ID。
	// 仅对 project.read 等可能产生大输出的工具启用。
	saveSnapshot func(id string, content []byte) error

	// suppressEventEmit 跳过 flushEvents/flushTurnEvents 里的 ctx.EmitEvent。
	// child-mode agent 的事件总线没有订阅者，所有输出通过显式 callable 转发给父 agent。
	suppressEventEmit bool

	// onFlushProgress 在 flushEvents 末尾被调用，让 child-mode agent
	// 有机会把累积进度（如 block.delta 文本）转发给父 agent。
	// 限速由调用方自己负责。
	onFlushProgress func(ctx actor.Context)

	// onToolHeartbeat 在工具执行阶段（phaseExecute）开始时调用，返回一个 stop
	// 函数，在工具执行结束时调用。child-mode 用它启动一个后台 ticker 定期刷新
	// atomic 活动时间戳，确保长时间静默工具（如 sleep、慢编译）期间父端
	// ChildAgentIdleTimeout 不会误判失联。返回 nil 表示无需 heartbeat。
	onToolHeartbeat func() func()

	// onAfterFlush 在 flushEvents 成功后调用，让 agent 刷新 atomic snapshot。
	// 确保断线重连时 agent.session.summary 能返回运行中 turn 的已完成 step。
	// 流式路径用防抖版本（takeSnapshotDebounced）避免每 chunk O(session) 拷贝。
	onAfterFlush func()

	// onAfterFlushForce 在 interaction 边界（emitStepImmediate）被调用：
	// 这里 snapshot 必须立即可见（ask_user 等待期间刷新页面），不能防抖。
	// 未设置时回退到 onAfterFlush。
	onAfterFlushForce func()

	// onCheckpoint 在 phaseCommit 成功后、turn 仍在运行时被调用，
	// 让 agent 把当前 steps 落盘，实现崩溃恢复。
	onCheckpoint func(ctx actor.Context)

	// onGrantPermission 在权限答案被允许时调用，传入 projectID 和 callableID
	// 以便 agent 将项目级授权持久化。
	onGrantPermission func(projectID, callableID string)

	// hasProjectApproval 检查某个 callableID 是否已在指定 project 下被授权。
	hasProjectApproval func(projectID, callableID string) bool

	// allowCallableScope verifies that a card-provided callable still belongs to
	// an enabled mounted CardRef at execution time.
	allowCallableScope func(callableID string) bool

	// 期间切换 yolo/bypass/permission 能立即生效，不受 turn 启动时烘焙的
	// CompiledContext.PermissionPolicy 快照限制。
	livePermissionMode func() string

	// worktreeRoot returns the agent's bound worktree root path, or "" when
	// unbound. Shell Dir injection prefers it over the main project root so
	// bound workers' shell commands run inside their worktree.
	worktreeRoot func() string

	// roots holds the configured project roots for outside-root permission
	// checks. Pre-loaded by initRoots at turn startup from project.info;
	// read-only after run() begins. Empty for read-only agents (no mutating
	// tools) since the roots branch in checkBatchPermission is unreachable.
	roots []string

	// consumePendingSubmits 在 ingestPendingMessages 时被调用，
	// 将 agent 层面挂起的用户消息加入 history 并返回它们。
	consumePendingSubmits func() []domain.ChatMessage

	// consumePendingUnitChange 在 run 循环的安全窗口被调用，
	// 取出 agent 层面挂起的 model unit 变更并返回给引擎。
	consumePendingUnitChange func() *domain.ModelUnit

	// consumePendingToolsChange 在 run 循环的安全窗口被调用（紧跟
	// consumePendingUnitChange 之后）。组件 mount/unmount/set_enabled 修改
	// 工具贡献后返回重新解析的完整工具面；无变更时返回 nil。
	consumePendingToolsChange func(model string) []domain.ToolSpec

	// actionCount 记录本次 turn 内已产生的动作数（step + execution）。
	actionCount int32

	// meta is the default source identifier written to assistant steps emitted by
	// this engine (e.g. "agent:Coder"). Supplied by the owning actor.
	meta string

	// 内联 turn 执行的核心可变状态
	history            []domain.ChatMessage    // 完整对话历史
	delta              []domain.ChatMessage    // 本轮新增消息，用于 UI
	pendingMessages    []domain.ChatMessage    // 用户新消息队列
	nextIdx            int32                   // 下一个消息序号
	summarySegments    []domain.SummarySegment // 摘要分段
	compactionEvents   []domain.CompactionEvent
	compactionRoundSeq int32

	// dispatchImagesAsText is set by recognizeHistoryImages when this
	// dispatch's unit is known text-only; buildDispatchHistory then replaces
	// recognized image blocks with their recognition text on the wire. Vision
	// primaries never set it, so they never read recognition fields.
	dispatchImagesAsText bool

	// onImageRecognized writes a one-shot recognition result back into the
	// actor-persisted step backing that history message (by message ID), so
	// recognition state survives engine restarts and rebuilds.
	onImageRecognized func(stepID string, blockIdx int, recognized bool, text string)

	// childDoneCh 接收 fork 子 agent 的结果。
	childDoneCh chan childResult

	// asyncPending 跟踪 Async=true 的 fork 子 agent（不扣留 batch，
	// 不参与 executeWaitChildren 的等待/超时/失联回收），由 agent_wait
	// 显式收割。
	asyncPending []pendingChild

	// asyncChildResults 记录本 turn 内已完成的 async fork 子 agent 终态，
	// 键为 ToolUseID。agent_wait 晚到收割时从这里读取。
	asyncChildResults map[string]asyncChildOutcome

	// onExploreResultLookup lets agent_wait resolve a child agent ID against
	// the agent's persisted ExploreResults (cross-turn retrieval: an async
	// child that finished after its parent turn ended).
	onExploreResultLookup func(agentID string) (domain.ExploreResult, bool)

	// resumeCh 在用户回答 permission 或 ask_user 问题时被触发。
	// 带缓冲，保证答案处理 handler 不会阻塞。
	resumeCh chan struct{}

	// resumeAnswer 在 resumeCh 被触发前写入的 JSON 答案载荷。
	resumeAnswer string

	// resumeRequestID 暂停时发放的规范 ID，答案必须回显该 ID，
	// 以拒绝过期的 prompt。
	resumeRequestID string

	// pauseRequested 为 true 时，下一次 phaseCommit 后进入 LoopPaused，
	// 阻塞等待 resumeCh（用户恢复）或 e.done（用户取消）。
	pauseRequested bool

	// pauseCtx 在 requestPause 时被 cancel，用于立即中断正在执行的工具，
	// 使暂停响应速度与取消一致。resumeUserPause 时重新创建，以便后续暂停仍能生效。
	pauseCtx    context.Context
	pauseCancel context.CancelFunc

	// onPause 在引擎进入 LoopPaused 时调用，让 agent 通知前端。
	onPause func()
	// onResume 在引擎从 LoopPaused 恢复时调用，让 agent 通知前端。
	onResume func()

	// onUnitChanged 在运行中成功应用 pendingChangeUnit 时调用，
	// 让 agent 更新 status.Unit 并通知前端。
	onUnitChanged func(unit domain.ModelUnit)

	// onResolvedUnit 在聚合器上报本次 dispatch 实际选中的 unit 时调用，
	// 让 agent 用真实执行的 model 覆盖 status.Unit（替代从请求 unit 取值）。
	onResolvedUnit func(unit domain.ModelUnit)

	// onRouteUnitFailed 在 selectDispatchStream 的某个 unit 候选 open 失败、
	// 即将轮换到下一候选时调用，让 agent 把该 unit 在主路由链中降级
	// （移到其余 unit 候选之后、兜底尾之前），下一次 dispatch 优先尝试
	// 未失败的 unit。回调不改变本次轮换本身。
	onRouteUnitFailed func(unit domain.ModelUnit)

	// tools 本次 turn 可用的工具规格，由 CompiledContext.Capabilities
	// 加上 ask_user（交互式场景，yolo 模式除外）组成。
	tools    []domain.ToolSpec
	allTools []domain.ToolSpec
	// baseTools 保存去掉 provider-native 声明后的工具规格，用于 model unit
	// 切换时重新计算当前模型支持的原生工具（例如 glm 的 web_search）。
	baseTools []domain.ToolSpec

	// forcedIO 是回合终局强制 IO toolcall 的当前契约（见
	// turn_engine_forced_io.go）。非 nil 时 buildDispatchRequest 用它覆盖
	// Tools/ToolChoice，phaseForcedDispatch 执行完一次强制往返后清空。
	forcedIO *forcedIOContract
	// forcedIOAttempted 保证每 turn 至多尝试一次强制 IO dispatch：无论
	// 成败，终局判定不再重入，防止 IO 卡常驻 doing 时回合死循环。
	forcedIOAttempted bool
	// onForcedIOContract 由 agent 层提供：查询当前 agent 名下 status=doing
	// 且声明 data.io 的任务卡，解析出强制 IO 契约。nil（测试）= 无契约。
	// 返回 (nil, nil) 表示没有契约（无 project / 无匹配卡）。
	onForcedIOContract func(ctx actor.Context) (*forcedIOContract, error)
	// onForcedIOComplete 在强制 toolcall 成功执行后由 agent 层落卡：
	// task_outputs 写 result、状态 doing→done。返回错误视为记账失败。
	onForcedIOComplete func(ctx actor.Context, cardID, resultJSON string) error

	// knownAppIDsCache caches the registered app ID set for this turn, fetched
	// lazily from appmanager.list to disambiguate dotted app IDs in app tool
	// callable IDs (see parseAppToolCallable). nil = not yet fetched.
	knownAppIDsCache []string

	// stepSeq 本次 turn 的单调 step 计数器（仅用于 ID 生成）。
	stepSeq int

	// allocSeq returns the next session-global step sequence number.
	// When set, emitStepEvent/emitStepImmediate use it for step.opened
	// and step.interaction_requested events so that Seq values remain
	// monotonic across turns. When nil (tests), falls back to stepSeq.
	allocSeq func() int64

	// stepByID 和 stepOrder 用于构建 turn 状态和发送 step 事件。
	stepByID  map[string]domain.TurnAction
	stepOrder []string

	// currentNode 当前 dispatch 活跃的 plan node，用于取消时停止它。
	currentNode plan.Node

	// pendingCalls 上一次 LLM dispatch 解析出的待执行工具调用。
	pendingCalls []pendingToolCall

	// batches 和 batchIdx 跟踪分区后的工具调用批次执行。
	batches  []toolExecutionBatch
	batchIdx int

	// batchSteps 当前批次对应的 step 条目。
	batchSteps []domain.TurnAction

	// permissionReason 当批次需要确认时存储原因说明。
	permissionReason string

	// iter LLM dispatch 轮数。
	iter int

	// emptyToolUseRetries counts consecutive dispatches where the LLM
	// returned stopReason=tool_use with zero parsed tool calls.
	emptyToolUseRetries int

	// iterText 最近一轮 dispatch 的 assistant 文本。
	iterText string

	// totalUsage 累计 token 用量。
	totalUsage *domain.UsageData

	// requestStats collects per-delivery telemetry for Turn.RequestStats.
	requestStats []domain.TurnRequestStat

	// finalText 累计 assistant 输出文本。
	finalText string

	// deltaCharCounts 自上次 dispatch 以来新增消息的字符分类计数（增量）。
	deltaCharCounts tokenest.CharCounts

	// deltaEstimatedTokens 自上次 dispatch 以来新增消息的估算 token 数（增量）。
	deltaEstimatedTokens int

	// lastDeltaCC / lastDeltaEst 保存最近一次 dispatch 前快照的增量值，
	// 供 recordDispatchSuccess 中的校准使用。
	lastDeltaCC  tokenest.CharCounts
	lastDeltaEst int

	// lastActualInputTokens 记录上一次 dispatch 的 LLM InputTokens，
	// 用于计算增量实际 token，避免把完整 prompt 与局部消息文本做比较。
	lastActualInputTokens int64

	// pendingChildren 跟踪所有在途 fork_child 调用。
	pendingChildren []pendingChild
	// pendingChildSteps lets the default loop reject progress emitted by a child
	// that belongs to a cancelled or previous turn.
	pendingChildSteps sync.Map

	// childProgressAt 记录每个子 agent 最后发送 explore_progress 的时间戳
	// (key=StepID → value=int64 unix nano)。handleExploreProgress (default loop)
	// 写入，earliestChildDeadline / handleChildTimeout (agent.exec loop) 读取。
	// sync.Map 自带并发安全，无需额外加锁。
	childProgressAt sync.Map

	// childLivenessFailures tracks consecutive liveness-check failures per
	// child (key=StepID → value=int). When a child's agent_status call fails
	// (deleted actor, crash, no response), the counter increments; on success
	// it resets. After childLivenessFailureThreshold consecutive failures the
	// child is evicted immediately instead of waiting for the full
	// ChildAgentIdleTimeout — this is how a deleted fork child unblocks the
	// parent promptly instead of stalling for 2 minutes.
	childLivenessFailures sync.Map

	// childProgressCh 在收到 explore_progress 时被 signal，唤醒 executeWaitChildren
	// 重新计算 idle deadline 并重置 timer。Buffered(1) + non-blocking send 确保
	// handleExploreProgress 永不阻塞。
	childProgressCh chan struct{}

	// maxDispatchRetries 是单次 dispatch 失败后的最大重试次数。
	maxDispatchRetries int

	// unitSlotRetryBudget is the extra retry budget applied on top of
	// maxDispatchRetries when the active slot pins a concrete unit (no
	// aggregator fallback). Zero defaults to unitSlotRetryBudget.
	unitSlotRetryBudget int

	// dispatchRetryStrategy 判断 dispatch 失败后是否重试及等待时间。
	// 可替换以便测试和注入配置。
	dispatchRetryStrategy func(attempt int, err error) (shouldRetry bool, backoff time.Duration)

	// fileChanges 累计本 turn 内所有文件工具产生的结构化变更，
	// 最终写入 Turn.FileChanges 供前端展示。
	fileChanges []domain.TurnFileChange
}

// run 是 agent.exec 自定义循环上的主入口。
// 它是 inline turn dispatch 的主执行循环。
//
// 核心流程图：
//
//	┌─────────────────┐
//	│   启动前取消检查  │
//	└────────┬────────┘
//	         ↓
//	┌─────────────────┐
//	│ 生成 turnID      │
//	│ 初始化 history   │
//	│ 初始化 tools     │
//	└────────┬────────┘
//	         ↓
//	┌─────────────────┐     失败/完成     ┌─────────────┐
//	│  loopState =    │ ───────────────→ │  phaseCommit │
//	│  LoopDispatch   │                  │  刷事件+结束  │
//	└────────┬────────┘                  └─────────────┘
//	         ↓
//	┌─────────────────────────────────────────────────────┐
//	│  内层循环：根据 loopState 调用对应 phase              │
//	│  Dispatch → Audit → Execute → Commit → (done?)      │
//	│  若 pendingMessages 非空，Commit 后会回到 Dispatch   │
//	└─────────────────────────────────────────────────────┘
type turnActorContext struct {
	actor.Context
	lifecycle context.Context
}

func (c turnActorContext) Lifecycle() context.Context { return c.lifecycle }

func (e *turnEngine) run(ctx actor.Context) error {
	turnCtx := e.turnContext(ctx.Lifecycle())
	ctx = turnActorContext{Context: ctx, lifecycle: turnCtx}

	startedModel := ""
	if e.startReq.Input.Unit != nil {
		startedModel = e.startReq.Input.Unit.Model
	}
	if e.runExited != nil {
		defer close(e.runExited)
	}
	defer e.releaseTurnContext()
	e.logger.Info("turnEngine: run started", "model", startedModel, "agentID", e.agentID)
	ctxDone := ctx.Lifecycle().Done()

	// 启动前快速检查是否已被取消。
	select {
	case <-ctxDone:
		e.logger.Info("turnEngine: cancelled before start")
		return nil
	case <-e.done:
		e.logger.Info("turnEngine: cancelled before start")
		return nil
	default:
	}

	turnID := e.turnID
	if turnID == "" {
		turnID = ctx.NewID().String()
	}
	e.startedAt = time.Now()

	// 从 CompiledContext 恢复历史、摘要、索引等状态。
	e.initHistoryFromCompiledContext(ctx)
	// 根据 Capabilities 和 PermissionPolicy 初始化可用工具。
	e.initTools()
	e.initRoots(ctx)

	// 初始化 dispatch 重试默认值（允许外部测试覆盖）。默认只重试一次，避免与 client 层重试叠加。
	if e.maxDispatchRetries == 0 {
		e.maxDispatchRetries = 5
	}

	// unitSlotRetryBudget extends the dispatch retry limit when the slot pins a
	// concrete unit. A locked unit has no fallback candidate, so a transient
	// provider hiccup should burn more retries on the same endpoint before
	// giving up (the alternative — failing the turn — is worse).
	if e.unitSlotRetryBudget == 0 {
		e.unitSlotRetryBudget = unitSlotRetryBudget
	}
	if e.dispatchRetryStrategy == nil {
		e.dispatchRetryStrategy = defaultDispatchRetryStrategy()
	}

	// ── 状态机主循环 ──
	e.setLoopState(ctx, turnID, LoopDispatch)

	// 外层 + 内层循环统一带 label，取消信号触发时 break 出两层，
	// 让下面 closeRemainingOpenSteps / reconcileOrphanToolUses / 最终
	// phaseCommit 等收尾跑完。早 return 会跳过这些收尾，导致持久化的
	// cancelled turn 缺 tool_result，下一轮 dispatch 触发 LLM 协议错误。
mainLoop:
	for {
		// 外层循环：当状态到达 Completed/Failed/Cancelled 等终态时退出。
		if e.loopState.IsTerminal() {
			break
		}

		// 内层循环：驱动一个完整回合（Dispatch → ... → Commit）。
		for !e.loopState.IsTerminal() {
			// 每次迭代前快速检查取消信号，保证内层循环能立即退出。
			select {
			case <-e.done:
				e.setLoopState(ctx, turnID, LoopCancelled)
				break mainLoop
			case <-ctxDone:
				e.setLoopState(ctx, turnID, LoopCancelled)
				break mainLoop
			default:
			}

			e.logger.Debug("turnEngine: loop", "state", e.loopState.String(), "iter", e.iter)
			switch e.loopState {
			case LoopDispatch:
				// Dispatch：构建历史、调用 LLM、解析输出。
				if err := e.phaseDispatch(ctx, turnID); err != nil {
					e.logger.Error("turnEngine: dispatch failed", "error", err.Error(), "agentID", e.agentID)
					e.failTurn(ctx, turnID, err)
				}

			case LoopAudit:
				// Audit：对工具调用分区、创建 step、检查权限。
				if err := e.phaseAudit(ctx, turnID); err != nil {
					e.logger.Error("turnEngine: audit failed", "error", err)
					e.reportDiagnosticIfSet(ctx, "agent", err.Error(), turnID)
					e.failTurn(ctx, turnID, err)
				}

			case LoopExecute:
				// Execute：执行当前批次，处理 ask_user/permission/fork_child。
				if err := e.phaseExecute(ctx, turnID); err != nil {
					e.logger.Error("turnEngine: execute failed", "error", err)
					e.reportDiagnosticIfSet(ctx, "agent", err.Error(), turnID)
					e.failTurn(ctx, turnID, err)
				}

			case LoopForcedDispatch:
				// ForcedDispatch：回合终局强制 IO toolcall——ToolChoice 钉死
				// 的额外一次 LLM 往返生成参数，校验后执行 callable。
				if err := e.phaseForcedDispatch(ctx, turnID); err != nil {
					e.logger.Error("turnEngine: forced IO dispatch failed", "error", err)
					e.reportDiagnosticIfSet(ctx, "agent", err.Error(), turnID)
					e.failTurn(ctx, turnID, err)
				}
			}

			// Commit：刷事件；如果是终止态且还有 pendingMessages，则回到 Dispatch。
			done, err := e.phaseCommit(ctx, turnID)
			if err != nil {
				e.logger.Error("turnEngine: commit failed", "err", err)
				e.reportDiagnosticIfSet(ctx, "agent", err.Error(), turnID)
				e.failTurn(ctx, turnID, err)
			}
			e.logger.Debug("turnEngine: loop done", "state", e.loopState.String(), "iter", e.iter)
			if done {
				break
			}

			// Pending model unit change: apply at the next safe judgment window
			// (all steps closed, same point where pause is handled).
			e.applyPendingUnitChange()

			// Pending component-snapshot change (mount/unmount/set_enabled):
			// refresh the tool surface at the same safe window so mid-turn
			// bundle mounts take effect for the remaining dispatches.
			e.applyPendingToolsChange()

			// 用户暂停：requestPause 已 cancel pauseCtx 中断了工具/流式输出，
			// 此处是安全点（所有 step 已闭合），阻塞等待恢复或取消。
			e.mu.Lock()
			shouldPause := e.pauseRequested
			e.mu.Unlock()
			if shouldPause {
				e.setLoopState(ctx, turnID, LoopPaused)
				if e.onPause != nil {
					e.onPause()
				}
				select {
				case <-e.resumeCh:
					e.mu.Lock()
					e.pauseRequested = false
					e.mu.Unlock()
					e.setLoopState(ctx, turnID, LoopDispatch)
					if e.onResume != nil {
						e.onResume()
					}
				case <-e.done:
					e.setLoopState(ctx, turnID, LoopCancelled)
					break mainLoop
				case <-ctxDone:
					e.setLoopState(ctx, turnID, LoopCancelled)
					break mainLoop
				}
			}
		}
	}

	// 兜底：关闭所有仍处于 running 的 step，确保前端 computeIsStreaming 能归零。
	// 任何错误路径若漏发了 step.error/step.closed（例如 dispatch 流式错误时
	// reasoning step 已 opened 但未被关闭），都会导致前端永久认为 turn 还在流。
	e.closeRemainingOpenSteps(ctx, turnID)

	// 兜底：修复 history 中的孤儿 tool_use（已写入但没有对应 tool_result）。
	// 这类孤儿来自 finalizeDispatch 写入 assistant(tool_use) 后 turn 因报错/取消
	// 退出、尚未走到 execute 的场景。孤儿若进入持久化或下一轮 dispatch 的 prompt，
	// 会违反 LLM 协议（assistant tool_use 必须紧跟 tool result）并造成连锁报错。
	// 这里在 run() 退出、持久化前就地修复，与 sanitizeMessages 的读时修复互补。
	e.reconcileOrphanToolUses(turnID)

	// 最终再 commit 一次，确保终止事件被刷出。
	_, _ = e.phaseCommit(ctx, turnID)

	e.logger.Info("turnEngine: run completed", "state", e.loopState.String())
	return nil
}

// closeRemainingOpenSteps 在 run() 退出前对所有仍处于 running 的 step
// 补发关闭事件。这是防止前端永久卡在"生成中"的兜底：
// 前端 computeIsStreaming 判定 turn 是否结束的唯一依据是
// "存在未 Closed 的 assistant step"，所以任何已 step.opened 但因报错
// 路径遗漏而未 step.closed/step.error 的 step 都会导致 UI 死锁。
//
// 取消场景例外：用户主动停止时，LLM/reasoning step 里已经流式输出的
// 文本是有效内容，应该保留。此时补发 step.closed 而不是 step.error，
// 避免前端把部分文本替换成错误消息。
func (e *turnEngine) closeRemainingOpenSteps(ctx actor.Context, turnID string) {
	isCancelled := e.loopState == LoopCancelled
	for _, id := range e.stepOrder {
		step, ok := e.stepByID[id]
		if !ok {
			continue
		}
		if step.State == "completed" || step.State == "failed" || step.State == "cancelled" {
			continue
		}
		e.logger.Warn("turnEngine: closing leftover open step on run exit",
			"stepID", id, "kind", step.Kind, "state", step.State, "cancelled", isCancelled)

		if isCancelled && (step.Kind == string(domain.TurnActionLLMCall) || step.Kind == string(domain.TurnActionReasoning)) {
			step.State = "cancelled"
			e.stepByID[id] = step
			e.emitStepEvent(domain.StepEvent{
				Kind:   "step.closed",
				StepID: id,
				TurnID: turnID,
			})
			continue
		}

		step.State = "failed"
		if step.Error == "" {
			step.Error = "turn ended while step was still open"
		}
		e.stepByID[id] = step
		e.emitStepEvent(domain.StepEvent{
			Kind:   "step.error",
			StepID: id,
			TurnID: turnID,
			Error:  step.Error,
		})
	}
}

// reconcileOrphanToolUses 在 run() 退出前就地修复 e.history 中的孤儿
// tool_use（assistant 已写入 tool_use 块但没有对应 tool_result 消息）。
//
// 孤儿来源：finalizeDispatch 的 Case 1（正常 tool_use 路径）会把
// assistant(tool_use) 写进 history 并 openToolCalls += N，随后 turn 可能在
// audit/execute 之前因报错或取消而退出，从未补上 tool_result。
//
// 修复策略（写时修复，直接改 e.history，与 sanitizeMessages 的读时修复互补）：
//  1. 收集 history 中所有 tool_use ID → 是否已有 tool_result 覆盖；
//  2. 对未覆盖的孤儿 tool_use，补一条 tool_result(isError) 消息，使协议平衡；
//  3. 重置 openToolCalls 为修复后的真实未覆盖数（正常应为 0）。
//
// 不剥除尾部 assistant 消息：那会丢弃已流式呈现给用户的文本/推理，
// 补 tool_result 足以让下一轮 dispatch 的 sanitizeMessages 不再触发剥离。
func (e *turnEngine) reconcileOrphanToolUses(turnID string) {
	resolved := make(map[string]struct{})
	for _, msg := range e.history {
		if msg.Role != domain.ChatRoleTool {
			continue
		}
		for _, b := range msg.Content {
			if b.Type == domain.ContentBlockToolResult && b.ToolUseID != "" {
				resolved[b.ToolUseID] = struct{}{}
			}
		}
	}

	var orphans []string
	for _, msg := range e.history {
		if msg.Role != domain.ChatRoleAssistant {
			continue
		}
		for _, b := range msg.Content {
			if b.Type != domain.ContentBlockToolUse {
				continue
			}
			if b.ToolUseID == "" {
				continue
			}
			if _, ok := resolved[b.ToolUseID]; !ok {
				orphans = append(orphans, b.ToolUseID)
			}
		}
	}

	if len(orphans) == 0 {
		return
	}

	e.logger.Warn("turnEngine: reconciling orphan tool_use blocks on run exit",
		"count", len(orphans), "toolUseIDs", orphans)

	for _, id := range orphans {
		e.appendMessage(domain.ChatMessage{
			ID:   turnID,
			Role: domain.ChatRoleTool,
			Content: []domain.ContentBlock{{
				Type:      domain.ContentBlockToolResult,
				ToolUseID: id,
				Text:      "tool result unavailable: turn ended before this tool call resolved",
				IsError:   true,
			}},
		})
	}

	// 修复后已无未覆盖 tool_use；强制对齐计数器，避免压缩被永久门控。
	e.openToolCalls = 0
}

// initHistoryFromCompiledContext 从 CompiledContext 复制消息、
// 为缺少索引的消息打戳，并在历史为空时（首 turn）用用户输入种子化历史。
func (e *turnEngine) initHistoryFromCompiledContext(ctx actor.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.logger.Info("turnEngine: init",
		"history", len(e.startReq.CompiledContext.Messages),
		"summarySegments", len(e.startReq.CompiledContext.SummarySegments))

	// 深拷贝历史与摘要，避免外部修改影响引擎内部状态。
	e.history = append([]domain.ChatMessage(nil), e.startReq.CompiledContext.Messages...)
	e.delta = nil
	e.nextIdx = e.startReq.CompiledContext.NextIdx
	e.summarySegments = append([]domain.SummarySegment(nil), e.startReq.CompiledContext.SummarySegments...)
	e.compactionEvents = nil
	e.compactionRoundSeq = 0

	// 保证 nextIdx 大于任何已有消息的索引。
	for _, msg := range e.history {
		if msg.Idx >= e.nextIdx {
			e.nextIdx = msg.Idx + 1
		}
	}

	// 对未打戳消息（Idx == 0）补 ID 和序号。
	for i := range e.history {
		if e.history[i].Idx == 0 {
			if e.history[i].ID == "" {
				e.history[i].ID = ctx.NewID().String()
			}
			e.stamp(&e.history[i])
		}
	}

	// Count initial history chars + estimate as the first delta for dispatch 0.
	e.deltaCharCounts = tokenest.CountMessageChars(e.history)
	estimator := tokenest.EstimateTokens
	if e.calibration != nil {
		estimator = e.calibration.EstimateTokens
	}
	for _, msg := range e.history {
		e.deltaEstimatedTokens += estimator(msg.ReasoningContent)
		for _, cb := range msg.Content {
			e.deltaEstimatedTokens += estimator(cb.Text)
			e.deltaEstimatedTokens += estimator(cb.Input)
		}
	}
	// 首 turn 且没有历史：用用户输入生成第一条 user 消息。
	if len(e.history) == 0 {
		var content []domain.ContentBlock
		if e.startReq.Input.Text != "" {
			content = append(content, domain.ContentBlock{Type: domain.ContentBlockText, Text: e.startReq.Input.Text})
		}
		for _, img := range e.startReq.Input.Images {
			content = append(content, domain.ContentBlock{Type: domain.ContentBlockImage, ImageURL: img.URL, MimeType: img.MimeType})
		}
		for _, att := range e.startReq.Input.Attachments {
			content = append(content, domain.ContentBlock{Type: domain.ContentBlockText, Text: fmt.Sprintf("[Attachment: %s (%s)]", att.Name, att.MimeType)})
		}
		if len(content) == 0 {
			content = append(content, domain.ContentBlock{Type: domain.ContentBlockText, Text: ""})
		}
		msg := domain.ChatMessage{
			ID:      ctx.NewID().String(),
			Role:    domain.ChatRoleUser,
			Content: content,
		}
		e.stamp(&msg)
		e.history = []domain.ChatMessage{msg}
	}
}

// initTools 从 Capabilities 填充工具列表；ask_user 仅在非 yolo 模式下挂载，
// 让 LLM 在需要时向用户提问。yolo 模式下不注入 ask_user，agent 必须
// 自主决策、不得向用户提问。
func (e *turnEngine) initTools() {
	if caps := e.startReq.CompiledContext.Capabilities; caps != nil {
		// Strip provider-native declarations; we will re-apply them based on
		// the current model in rebuildToolsForModel so that mid-turn model
		// switches can drop/add tools like web_search appropriately.
		e.baseTools = filterNativeTools(caps.PrimitiveTools)
	}
	model := ""
	if e.startReq.Input.Unit != nil {
		model = e.startReq.Input.Unit.Model
	}
	e.rebuildToolsForModel(model)
}

// rebuildToolsForModel rebuilds e.tools/allTools from e.baseTools plus any
// provider-native tools supported by the given model name.
func (e *turnEngine) rebuildToolsForModel(model string) {
	e.tools = nativeToolRegistry.AppendWebSearch(e.baseTools, model)
	// ask_user is blocked in autonomous modes (yolo/autopilot): the agent must
	// act on its own; every other mode exposes it so the LLM can ask the user.
	if !e.isAutonomousMode() {
		e.tools = append(e.tools, askUserToolSpec())
	}
	e.allTools = e.tools
}

// isYoloMode reports whether the live permission mode is "yolo" — the agent
// acts fully autonomously: tool calls are auto-approved, plan_submit and
// goal_submit/goal_card_submit are auto-approved, and ask_user is blocked.
func (e *turnEngine) isYoloMode() bool {
	return e.livePermissionMode != nil && e.livePermissionMode() == "yolo"
}

// isAutopilotMode reports whether the live permission mode is "autopilot" —
// like yolo (plan/goal/workflow auto-approved, ask_user blocked) but tool
// calls are vetted by the fast model instead of unconditionally allowed.
func (e *turnEngine) isAutopilotMode() bool {
	return e.livePermissionMode != nil && e.livePermissionMode() == "autopilot"
}

// isAutonomousMode reports whether the agent acts fully autonomously:
// plan/goal/workflow submissions are auto-approved and ask_user is blocked.
// Both "yolo" and "autopilot" qualify; the difference is tool-call gating.
func (e *turnEngine) isAutonomousMode() bool {
	return e.isYoloMode() || e.isAutopilotMode()
}

// filterNativeTools returns a copy of tools with provider-native specs (Type
// non-empty) removed. Standard function/plugin tools have an empty Type.
func filterNativeTools(tools []domain.ToolSpec) []domain.ToolSpec {
	if len(tools) == 0 {
		return nil
	}
	filtered := make([]domain.ToolSpec, 0, len(tools))
	for _, t := range tools {
		if t.Type == "" {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

// initRoots pre-loads project roots during turn initialization so that
// checkBatchPermission can read e.roots without blocking on a planner call.
// Read-only agents (allTools EffectNone) skip the call entirely — they never
// reach the roots branch in checkBatchPermission.
func (e *turnEngine) initRoots(ctx actor.Context) {
	hasMutating := false
	for _, t := range e.allTools {
		if t.EffectKind != "" && t.EffectKind != string(domain.EffectNone) {
			hasMutating = true
			break
		}
	}
	if !hasMutating {
		return
	}

	svcRef, ok := ctx.LookupService("project")
	if !ok {
		return
	}
	callCtx, cancel := context.WithTimeout(e.turnContext(ctx.Lifecycle()), domain.DefaultInvokeTimeout)
	defer cancel()
	result, err := ctx.Planner().Call(callCtx, svcRef, "project.info", nil).Await()
	if err != nil {
		e.logger.Warn("turnEngine: initRoots failed", "error", err)
		return
	}
	info := decodeProjectInfo(result)
	for _, r := range info.Roots {
		if r.Path != "" {
			e.roots = append(e.roots, util.NormalizePath(r.Path))
		}
	}
}

// trackOpenStepEvent 把事件追加到对应 step 的未关闭事件列表。
// 一旦收到 step.closed / step.error 就清理该 step 的事件（最终状态已落盘）。
func (e *turnEngine) trackOpenStepEvent(ev domain.StepEvent) {
	switch ev.Kind {
	case "step.closed", "step.error":
		delete(e.openStepEvents, ev.StepID)
	default:
		if e.openStepEvents == nil {
			e.openStepEvents = make(map[string][]domain.StepEvent)
		}
		e.openStepEvents[ev.StepID] = append(e.openStepEvents[ev.StepID], ev)
	}
}

// emitChildProgressEvent 缓冲快照式的子 agent 进度事件（step.execution_progress）。
// 与 emitStepEvent 的唯一区别：openStepEvents 中该 step 旧的 execution_progress
// 条目被替换而非追加。每条进度 payload 都是全量快照，重连回放只需最新一条；
// 若逐条追加，子 agent 每 ~200ms 一次的 flush 会让 openStepEvents 随时间线性膨胀
// （且单条随 SummaryText 增大），使 handleExploreProgress 里的 takeSnapshot 拷贝
// 成本变为 O(n²)，最终拖慢 owner loop、塞满 owner queue（容量 64），后续进度
// 投递瞬时失败被丢弃，UI 进度冻结。
func (e *turnEngine) emitChildProgressEvent(ev domain.StepEvent) {
	ev.Meta = e.defaultMeta(ev)
	e.stepEventMu.Lock()
	e.stepEventSeq++
	ev.EventSeq = e.stepEventSeq
	e.stepEvents = append(e.stepEvents, ev)
	if e.openStepEvents == nil {
		e.openStepEvents = make(map[string][]domain.StepEvent)
	}
	kept := e.openStepEvents[ev.StepID]
	n := 0
	for _, existing := range kept {
		if existing.Kind == "step.execution_progress" {
			continue
		}
		kept[n] = existing
		n++
	}
	e.openStepEvents[ev.StepID] = append(kept[:n], ev)
	e.stepEventMu.Unlock()
}

// defaultMeta returns the source identifier for a step event emitted by this
// engine. It respects any caller-supplied Meta and falls back to the engine's
// configured agent meta or a role-based default.
func (e *turnEngine) defaultMeta(ev domain.StepEvent) string {
	if ev.Meta != "" {
		return ev.Meta
	}
	if ev.StepType == "user_inject" {
		return "user"
	}
	switch ev.Role {
	case "assistant", "tool":
		if e.meta != "" {
			return e.meta
		}
		return "agent"
	case "system":
		return "system"
	case "user":
		return "user"
	default:
		return ""
	}
}

// emitStepEvent 缓冲 step 事件，稍后统一 flush。
func (e *turnEngine) emitStepEvent(ev domain.StepEvent) {
	ev.Meta = e.defaultMeta(ev)
	ev.Time = time.Now().UTC().Format(time.RFC3339Nano)
	e.stepEventMu.Lock()
	e.stepEventSeq++
	ev.EventSeq = e.stepEventSeq
	if ev.Kind == "step.opened" && e.allocSeq != nil {
		ev.Seq = e.allocSeq()
	}
	e.stepEvents = append(e.stepEvents, ev)
	e.trackOpenStepEvent(ev)
	e.stepEventMu.Unlock()
}

// emitStepImmediate assigns an EventSeq and emits the event immediately without
// buffering. Use for interaction events (interaction_requested, interaction_resolved)
// that must reach the frontend AND the atomic snapshot before the turn engine
// blocks waiting for a user response.
//
// 契约（镜像 flushEvents，但绕过缓冲）：
//  1. 前置 flushEvents：把 phaseAudit / applyAskUserAnswer 等缓冲的 step.opened /
//     block.appended / step.closed 冲出去，保证 SSE 时序（interaction 永远在前置
//     step 之后到达）且前置 step 已经写入 atomic snapshot。
//  2. onStepEvent：把事件应用到 a.steps 内存。
//  3. ctx.EmitEvent：推送给前端 SSE 通道。
//  4. onAfterFlush：刷新 atomic snapshot，让 session.summary（浏览器刷新 / 重连）
//     立即看到 interaction step。修复"ask_user 等待期间刷新页面卡片消失"的 bug。
//
// EventSeq 与 emitStepEvent 共享，monotonicity 跨缓冲/立即事件保持。
func (e *turnEngine) emitStepImmediate(ctx actor.Context, ev domain.StepEvent) error {
	// 1. 前置 flush：保证 interaction 事件之前的 step 状态已投递且写入 snapshot。
	//    避免"ask_user 卡片显示但 tool_call step 缺失"的不一致。
	if err := e.flushEvents(ctx); err != nil {
		return fmt.Errorf("emitStepImmediate pre-flush: %w", err)
	}

	// 2. 分配 EventSeq + 维护 openStepEvents。
	e.stepEventMu.Lock()
	e.stepEventSeq++
	ev.EventSeq = e.stepEventSeq
	ev.Meta = e.defaultMeta(ev)
	ev.Time = time.Now().UTC().Format(time.RFC3339Nano)
	e.trackOpenStepEvent(ev)
	e.stepEventMu.Unlock()
	if ev.Kind == "step.interaction_requested" && e.allocSeq != nil {
		ev.Seq = e.allocSeq()
	}

	// 3. 应用到 a.steps 内存。
	if e.onStepEvent != nil {
		e.onStepEvent(ev)
	}

	// 4. Persist interaction steps before advertising them. The interaction
	// callback records its pending state in the same mailbox checkpoint, so a
	// crash cannot restore this wait as a generic recovery pause.
	if ev.Kind == "step.interaction_requested" && e.onInteractionRequested != nil {
		e.onInteractionRequested(ctx, ev.TurnID, ev.StepID, ev.RequestID, ev.InteractionType, ev.Task)
	}
	if ev.Kind == "step.interaction_requested" && e.onCheckpoint != nil {
		e.onCheckpoint(ctx)
	}

	// 5. 推送 SSE。
	if err := ctx.EmitEvent("step", ev); err != nil {
		return fmt.Errorf("emitStepImmediate emit %s: %w", ev.Kind, err)
	}
	if e.onEventForward != nil {
		e.onEventForward(ctx, "step", ev)
	}

	// 6. 刷新 atomic snapshot：镜像 emitPlanApprovalEvent 末尾 takeSnapshot 的模式，
	//    让 session.summary 在刷新/重连时立即看到 interaction step。
	//    用 force 通道绕过流式防抖——interaction 等待期间 snapshot 必须新鲜。
	if e.onAfterFlushForce != nil {
		e.onAfterFlushForce()
	} else if e.onAfterFlush != nil {
		e.onAfterFlush()
	}
	return nil
}

// flushEvents 将缓冲的 stepEvents 刷到 agent 状态回调和事件订阅者。
// 返回遇到的第一个错误；调用方必须检查并在无法广播时中止 turn。
//
// 数据流：
//
//	stepEvents → onStepEvent(state callback)
//	          → ctx.EmitEvent("step", ev) (若未 suppress)
//	          → onFlushProgress (child-mode 转发进度)
func (e *turnEngine) flushEvents(ctx actor.Context) error {
	e.stepEventMu.Lock()
	if len(e.stepEvents) == 0 {
		e.stepEventMu.Unlock()
		return nil
	}
	stepEvents := e.stepEvents
	e.stepEvents = nil
	e.stepEventMu.Unlock()

	// 1. 先应用到本地 agent 状态。
	if e.onStepEvent != nil {
		for _, ev := range stepEvents {
			e.onStepEvent(ev)
			if e.onToolResult != nil && ev.Kind == "block.appended" && ev.Block != nil && ev.Block.Type == domain.ContentBlockToolResult {
				e.onToolResult(ctx, ev)
			}
		}
	}
	// 2. 再广播到事件总线；失败时保留未发送事件。
	if !e.suppressEventEmit {
		for i, ev := range stepEvents {
			if err := ctx.EmitEvent("step", ev); err != nil {
				e.logger.Error("turnEngine: step emit failed", "err", err, "kind", ev.Kind)
				e.stepEventMu.Lock()
				e.stepEvents = append([]domain.StepEvent{ev}, stepEvents[i+1:]...)
				e.stepEventMu.Unlock()
				return fmt.Errorf("emit step %s: %w", ev.Kind, err)
			}
			if e.onEventForward != nil {
				e.onEventForward(ctx, "step", ev)
			}
		}
	}
	// 3. child-mode 进度转发钩子。
	if e.onFlushProgress != nil {
		e.onFlushProgress(ctx)
	}
	// 4. 刷新 atomic snapshot：前端断线重连依赖它补漏 step。
	if e.onAfterFlush != nil {
		e.onAfterFlush()
	}
	return nil
}

// bufferTurnEvent 追加一个 turn 级别事件并分配单调 Seq。
// 线程安全：可被默认循环（handleTurnExploreProgress）和自定义循环
// （flushTurnEvents）并发使用。
func (e *turnEngine) bufferTurnEvent(ev domain.TurnEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	// TurnEvent.Seq removed after flattening; no per-event sequencing needed.
	e.turnEvents = append(e.turnEvents, ev)
}

// flushTurnEvents 通过 ctx.EmitEvent("turn", ...) 发送缓冲的 turn 级别事件。
// 线程安全：可被默认循环和自定义循环并发使用。
func (e *turnEngine) flushTurnEvents(ctx actor.Context) error {
	e.mu.Lock()
	if len(e.turnEvents) == 0 {
		e.mu.Unlock()
		return nil
	}
	turnEvents := e.turnEvents
	e.turnEvents = nil
	e.mu.Unlock()

	if e.suppressEventEmit {
		return nil
	}

	for i, ev := range turnEvents {
		e.logger.Info("turnEngine: emitting turn event", "kind", ev.Kind, "turnId", ev.TurnID)
		if err := ctx.EmitEvent("turn", ev); err != nil {
			e.logger.Error("turnEngine: emit turn event failed", "kind", ev.Kind, "error", err)
			// 重新缓冲未发送事件。
			e.mu.Lock()
			e.turnEvents = append(turnEvents[i:], e.turnEvents...)
			e.mu.Unlock()
			return fmt.Errorf("emit turn %s: %w", ev.Kind, err)
		}
		e.logger.Info("turnEngine: turn event emitted ok", "kind", ev.Kind)
	}
	return nil
}

// setLoopState 更新状态机当前阶段。非法转换会被记录并忽略，防止终态被
// 意外复活（例如 Failed/Cancelled 被 pending message 改回 Dispatch）。
func (e *turnEngine) setLoopState(_ actor.Context, _ string, state TurnLoopState) {
	if !e.loopState.CanTransition(state) {
		e.logger.Warn("turnEngine: illegal state transition rejected", "from", e.loopState, "to", state)
		return
	}
	e.loopState = state
}

// failTurn 标记 turn 为失败，并记录导致失败的错误信息，供 getDelta() 回流。
func (e *turnEngine) failTurn(ctx actor.Context, turnID string, err error) {
	// 取消（用户经 handleTurnCancel 停止，或生命周期关闭）不是失败：
	// 转入 LoopCancelled，使 getDelta 报告正确的终止态。当取消发生在 dispatch
	// 中途（runDispatch 返回 context.Canceled）时尤其关键——否则 turn 会被
	// 当作 failed 提交并带 "context canceled" 错误。
	if errors.Is(err, context.Canceled) {
		// Cancel is a lifecycle event, not an error. Clear any stale error so
		// getDelta() never leaks a pre-cancel failure into the cancelled record.
		e.turnError = ""
		e.setLoopState(ctx, turnID, LoopCancelled)
		return
	}
	if err != nil && e.turnError == "" {
		e.turnError = err.Error()
	}
	e.setLoopState(ctx, turnID, LoopFailed)
}

// turnErrorForState returns the recorded error only when the engine is in the
// failed state. Terminal completed/cancelled states intentionally return an
// empty error so a stale error from an earlier segment cannot leak into the
// final record.
func (e *turnEngine) turnErrorForState() string {
	if e.loopState == LoopFailed {
		return e.turnError
	}
	return ""
}

// AcceptingInjects reports whether the engine is still willing to take user
// or peer messages into the current turn. It is safe to call from any loop.
func (e *turnEngine) AcceptingInjects() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.acceptingInjects
}

// stamp 给消息分配 Idx 和时间戳，并推进 nextIdx。
func (e *turnEngine) stamp(msg *domain.ChatMessage) {
	msg.Idx = e.nextIdx
	msg.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	e.nextIdx++
}

// cancel 向引擎发送停止信号。
func (e *turnEngine) cancel() {
	e.mu.Lock()
	if !e.cancelled {
		e.cancelled = true
		if e.done != nil {
			close(e.done)
		}
		if e.turnCancel != nil {
			e.turnCancel()
		}
	}
	// A cancelled engine is exiting and will never consume PendingSubmits.
	// Flip acceptingInjects so handleChatSubmit does not route a fresh user
	// message into the dying engine (the message would be silently lost,
	// leaving the agent stuck with an empty TurnActorID).
	e.acceptingInjects = false
	e.mu.Unlock()
}

// requestPause 请求引擎暂停。立即 cancel pauseCtx 以中断正在执行的工具，
// 使暂停响应速度与取消一致。工具返回后，主循环的 pause 检查点会进入 LoopPaused。
func (e *turnEngine) requestPause() {
	e.mu.Lock()
	e.pauseRequested = true
	if e.pauseCancel != nil {
		e.pauseCancel()
	}
	e.mu.Unlock()
}

// pauseDone 返回 pauseCtx 的 Done 通道。如果 pauseCtx 未初始化（测试场景），
// 返回 nil，在 select 中永远阻塞（等效于无暂停能力）。
func (e *turnEngine) pauseDone() <-chan struct{} {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.pauseCtx == nil {
		return nil
	}
	return e.pauseCtx.Done()
}

// applyPendingUnitChange consumes a staged model unit change from the agent
// and applies it to the next dispatch iteration. It runs at the same safe
// judgment window as pause handling (after phaseCommit, with all steps closed).
func (e *turnEngine) applyPendingUnitChange() {
	if e.consumePendingUnitChange == nil {
		return
	}
	unitPtr := e.consumePendingUnitChange()
	if unitPtr == nil {
		return
	}
	e.startReq.Input.Unit = agentUnitPtr(*unitPtr)
	// The unit is the single selection primitive. resolveTargets reads
	// e.primarySlot (a turn-start snapshot) to pin a concrete unit, and
	// selectDispatchStream then overrides req.Unit with that slot's unit. A
	// mid-turn switch that only updates startReq.Input.Unit is silently
	// discarded by the stale slot — so rebuild the slot from the new unit to
	// keep both sources consistent.
	e.primarySlot = slotFromUnit(*unitPtr)
	// Reconcile provider-native tools for the new model so a mid-turn model
	// switch does not carry over unsupported native tools (e.g. glm's
	// web_search being sent to Kimi).
	if e.baseTools != nil {
		e.rebuildToolsForModel(unitPtr.Model)
	}
	if e.logger != nil {
		e.logger.Info("turnEngine: unit switched", "model", unitPtr.Model, "provider", unitPtr.Provider)
	}
	if e.onUnitChanged != nil {
		e.onUnitChanged(*unitPtr)
	}
}

// applyPendingToolsChange consumes a staged component-snapshot change from the
// agent and re-resolves the turn's tool surface. Mirrors applyPendingUnitChange:
// it runs at the safe judgment window (all steps closed), so tools contributed
// by a bundle mounted mid-turn become visible and resolvable for the next LLM
// dispatch iteration of the same turn.
func (e *turnEngine) applyPendingToolsChange() {
	if e.consumePendingToolsChange == nil {
		return
	}
	model := ""
	if e.startReq.Input.Unit != nil {
		model = e.startReq.Input.Unit.Model
	}
	tools := e.consumePendingToolsChange(model)
	if tools == nil {
		return
	}
	e.baseTools = filterNativeTools(tools)
	e.rebuildToolsForModel(model)
	if e.logger != nil {
		e.logger.Info("turnEngine: tool surface refreshed after component change", "tools", len(e.tools))
	}
}

// resumeUserPause 从用户暂停中恢复，唤醒被阻塞的 run() 循环。
// 同时重新创建 pauseCtx，以便后续暂停仍能中断工具。
func (e *turnEngine) resumeUserPause(parent context.Context) {
	e.initTurnContext(parent)
	e.mu.Lock()
	// 重新创建 pause context，让后续的 pause 能再次中断工具。
	e.pauseCtx, e.pauseCancel = context.WithCancel(e.turnCtx)
	select {
	case e.resumeCh <- struct{}{}:
	default:
	}
	e.mu.Unlock()
}

func (e *turnEngine) initTurnContext(parent context.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.turnCtx != nil {
		return
	}
	if parent == nil {
		parent = context.Background()
	}
	e.turnCtx, e.turnCancel = context.WithCancel(parent)
	if e.cancelled {
		e.turnCancel()
	}
}

func (e *turnEngine) turnContext(parent context.Context) context.Context {
	e.initTurnContext(parent)
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.turnCtx
}

func (e *turnEngine) releaseTurnContext() {
	e.mu.RLock()
	cancel := e.turnCancel
	e.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
}

// lifecycleWithCancel returns a per-I/O context derived from the turn context.
// Pause cancels only this operation; turn or actor cancellation propagates via
// the parent turn context.
func (e *turnEngine) lifecycleWithCancel(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(e.turnContext(parent))
	pauseDone := e.pauseDone()
	if pauseDone == nil {
		return ctx, cancel
	}
	safeGo("lifecycle_watcher", func() {
		select {
		case <-pauseDone:
			cancel()
		case <-ctx.Done():
		}
	})
	return ctx, cancel
}

// waitExitTimeout caps how long waitExit blocks the agent's default loop.
// cancel() closes e.done and cancels the turn context, which should wake any
// context-aware I/O within seconds. If run() is wedged on something that
// ignores cancellation (e.g. an unresponsive streaming HTTP body), the old
// unbounded select froze the entire agent actor — no further message could be
// processed, reproducing the "agent stuck after stop" bug.
const waitExitTimeout = 10 * time.Second

// waitExit 阻塞直到 run() 返回。handleTurnCancel 在 default loop 上调用
// cancel 后等待收尾完成，确保 writeCancelledToolResults +
// reconcileOrphanToolUses 已经把 tool_result 写进 a.steps / e.history，
// 否则持久化的 cancelled turn 仍会带孤儿 tool_use。
//
// 调用前必须确认 run() 真的已经启动（即 handleRun 已经设置了 e.runExited
// 并进入 e.run）。对未启动 / 已退出的 engine 调用是 no-op。
func (e *turnEngine) waitExit() {
	if e.runExited == nil {
		return
	}
	select {
	case <-e.runExited:
	case <-time.After(waitExitTimeout):
		e.logger.Warn("turnEngine: waitExit timed out, engine run() may be stuck")
	}
}

// isCancelled 报告引擎是否已被取消。
func (e *turnEngine) isCancelled() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cancelled
}

// appendMessages 将用户消息排队到 pendingMessages，等待下一次 dispatch 消费。
func (e *turnEngine) appendMessages(msgs []domain.ChatMessage) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pendingMessages = append(e.pendingMessages, msgs...)
}

// flushPendingMessages 取出并清空 pendingMessages 队列。
func (e *turnEngine) flushPendingMessages() []domain.ChatMessage {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.pendingMessages) == 0 {
		return nil
	}
	flushed := e.pendingMessages
	e.pendingMessages = nil
	return flushed
}

// deliverChildResult 将 fork 子 agent 的结果转发进引擎循环。
func (e *turnEngine) deliverChildResult(result childResult) {
	select {
	case e.childDoneCh <- result:
	default:
		e.logger.Warn("turnEngine: child result dropped, channel full", "toolUseID", result.ToolUseID)
	}
}

// receiveAnswer 存储用户答案并唤醒 dispatch 循环。
// 如果引擎未在等待答案，则丢弃。
func (e *turnEngine) receiveAnswer(requestID, answer string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.resumeRequestID == "" {
		return false
	}
	if requestID != "" && requestID != e.resumeRequestID {
		return false
	}
	e.resumeAnswer = answer
	select {
	case e.resumeCh <- struct{}{}:
	default:
	}
	return true
}

// appendMessage 给消息打戳并追加到 history 和 delta。
// 超长 tool_result 在此处 spill：全文落盘，history 中只留预览 + 取回指引。
func (e *turnEngine) appendMessage(msg domain.ChatMessage) {
	e.spillMessageIfNeeded(&msg)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stamp(&msg)
	e.history = append(e.history, msg)
	e.delta = append(e.delta, msg)
	// Incremental char counting for calibration.
	cc := tokenest.CountMessageChars([]domain.ChatMessage{msg})
	e.deltaCharCounts.Latin += cc.Latin
	e.deltaCharCounts.CJK += cc.CJK
	e.deltaCharCounts.Other += cc.Other
	if e.calibration != nil {
		e.deltaEstimatedTokens += e.calibration.EstimateTokens(msg.ReasoningContent)
		for _, cb := range msg.Content {
			e.deltaEstimatedTokens += e.calibration.EstimateTokens(cb.Text)
			e.deltaEstimatedTokens += e.calibration.EstimateTokens(cb.Input)
		}
	} else {
		e.deltaEstimatedTokens += tokenest.EstimateTokens(msg.ReasoningContent)
		for _, cb := range msg.Content {
			e.deltaEstimatedTokens += tokenest.EstimateTokens(cb.Text)
			e.deltaEstimatedTokens += tokenest.EstimateTokens(cb.Input)
		}
	}
}

// foldHistory collapses old messages in e.history (except the most recent N)
// into a single user message containing a truncated summary. This reduces the
// request body size without discarding information entirely.
func (e *turnEngine) foldHistory() {
	const keepRecent = 20
	if len(e.history) <= keepRecent {
		return
	}

	// Avoid nested folding if the first message is already a folded summary.
	if len(e.history) > 0 && len(e.history[0].Content) > 0 &&
		strings.HasPrefix(e.history[0].Content[0].Text, "[Collapsed context]") {
		return
	}

	splitIdx := len(e.history) - keepRecent

	var parts []string
	for _, msg := range e.history[:splitIdx] {
		for _, cb := range msg.Content {
			if cb.Text != "" {
				line := msg.Role + ": " + cb.Text
				// Truncate only tool_result messages.
				if msg.Role == "tool" && len(line) > 200 {
					line = line[:200] + "..."
				}
				parts = append(parts, line)
			}
		}
	}
	if len(parts) == 0 {
		e.history = e.history[splitIdx:]
		return
	}

	collapsed := "[Collapsed context]\n" + strings.Join(parts, "\n")
	collapsed = compaction.TruncateSafe(collapsed, 4000, "\n"+compaction.TruncationSuffix)

	summaryMsg := domain.ChatMessage{
		Role:    "user",
		Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: collapsed}},
	}
	e.stamp(&summaryMsg)
	e.history = append([]domain.ChatMessage{summaryMsg}, e.history[splitIdx:]...)
}

// appendFileChanges 把单次工具调用的文件变更合并到 turn 级累计列表。
// 相同路径的后续变更会覆盖前者，保证最终 diff 与磁盘最终状态一致。
func (e *turnEngine) appendFileChanges(changes []domain.TurnFileChange) {
	if len(changes) == 0 {
		return
	}
	if e.fileChanges == nil {
		e.fileChanges = make([]domain.TurnFileChange, 0, len(changes))
	}
	for _, c := range changes {
		found := false
		for i := range e.fileChanges {
			if e.fileChanges[i].Path == c.Path {
				e.fileChanges[i] = c
				found = true
				break
			}
		}
		if !found {
			e.fileChanges = append(e.fileChanges, c)
		}
	}
}

// getDelta 返回当前累积输出和压缩状态的快照。
func (e *turnEngine) getDelta() domain.AgentTurnCompleteReq {
	e.mu.RLock()
	defer e.mu.RUnlock()

	state := loopStateString(e.loopState)
	var completedAt string
	if e.loopState.IsTerminal() {
		completedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}

	return domain.AgentTurnCompleteReq{
		Turn: domain.Turn{
			ID:           e.turnID,
			Role:         "assistant",
			State:        state,
			Usage:        e.totalUsage,
			RequestStats: append([]domain.TurnRequestStat(nil), e.requestStats...),
			Timestamp:    e.startedAt.Format(time.RFC3339),
			Unit:         e.startReq.Input.Unit,
			StartedAt:    e.startedAt.Format(time.RFC3339),
			CompletedAt:  completedAt,
			FileChanges:  append([]domain.TurnFileChange(nil), e.fileChanges...),
			Assessment:   cloneTurnAssessment(e.assessment),
			// Only LoopFailed may carry an error; completed/cancelled must not
			// leak a stale turnError (e.g. a resumed turn or a cancel race).
			Error: e.turnErrorForState(),
		},
		Steps:            messagesToUISteps(e.delta, e.turnID),
		NextIdx:          e.nextIdx,
		SummarySegments:  append([]domain.SummarySegment(nil), e.summarySegments...),
		CompactionEvents: append([]domain.CompactionEvent(nil), e.compactionEvents...),
	}
}

func cloneTurnAssessment(in *gen.TurnAssessment) *gen.TurnAssessment {
	if in == nil {
		return nil
	}
	out := *in
	out.Evidence = append([]string(nil), in.Evidence...)
	out.Outputs = cloneStringAnyMap(in.Outputs)
	return &out
}

// cloneStringAnyMap returns a shallow copy of a map[string]any, or nil when the
// input is nil/empty. Used by cloneTurnAssessment so the captured Outputs are
// not shared by reference with the caller's input map.
func cloneStringAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// buildUnfinishedTaskReminderText constructs the reminder message appended
// to the conversation when the LLM ends its turn with tasks still unfinished.
// The LLM sees this as its own assistant message in the next dispatch and is
// prompted to check whether each task is complete, should be cancelled, or
// needs continued work.
func buildUnfinishedTaskReminderText(tasks []gen.TurnTask) string {
	var sb strings.Builder
	sb.WriteString("[Task Reminder] The following tasks are still unfinished. ")
	sb.WriteString("Please review each one and decide: mark it completed if done, cancel it if no longer relevant, or continue working on it.\n")
	for _, t := range tasks {
		icon := "◯"
		if t.Status == "in_progress" {
			icon = "◐"
		}
		sb.WriteString(fmt.Sprintf("\n%s %s (status: %s, id: %s)", icon, t.Subject, t.Status, t.ID))
		if t.ActiveForm != "" {
			sb.WriteString(fmt.Sprintf(" — %s", t.ActiveForm))
		}
	}
	return sb.String()
}

func loopStateString(s TurnLoopState) string {
	switch s {
	case LoopCompleted:
		return "completed"
	case LoopFailed:
		return "failed"
	case LoopCancelled:
		return "cancelled"
	case LoopPaused:
		return "paused"
	default:
		return "running"
	}
}

// orderedSteps 按 stepOrder 从 stepByID 中重建有序 step 切片。
func orderedSteps(stepByID map[string]domain.TurnAction, stepOrder []string) []domain.TurnAction {
	steps := make([]domain.TurnAction, 0, len(stepOrder))
	for _, id := range stepOrder {
		if step, ok := stepByID[id]; ok {
			steps = append(steps, step)
		}
	}
	return steps
}

// messagesToUISteps 把 delta 消息列表转换成 UI Step 列表。
func messagesToUISteps(msgs []domain.ChatMessage, turnID string) []domain.Step {
	uiSteps := make([]domain.Step, 0, len(msgs))
	for _, msg := range msgs {
		uiSteps = append(uiSteps, chatMessageToStep(msg, turnID))
	}
	return uiSteps
}

// eventKinds 提取 StepEvent 切片中所有不同 kind 的逗号连接字符串（仅用于日志）。
func eventKinds(events []domain.StepEvent) string {
	if len(events) == 0 {
		return ""
	}
	seen := make(map[string]struct{}, len(events))
	var kinds []string
	for _, ev := range events {
		if _, ok := seen[ev.Kind]; !ok {
			seen[ev.Kind] = struct{}{}
			kinds = append(kinds, ev.Kind)
		}
	}
	return strings.Join(kinds, ",")
}

// eventKindsTurn 提取 TurnEvent 切片中所有不同 kind 的逗号连接字符串（仅用于日志）。
func eventKindsTurn(events []domain.TurnEvent) string {
	if len(events) == 0 {
		return ""
	}
	seen := make(map[string]struct{}, len(events))
	var kinds []string
	for _, ev := range events {
		if _, ok := seen[ev.Kind]; !ok {
			seen[ev.Kind] = struct{}{}
			kinds = append(kinds, ev.Kind)
		}
	}
	return strings.Join(kinds, ",")
}

// reportDiagnosticIfSet is a convenience wrapper that reports a diagnostic
// if the onReportDiagnostic callback is set.
func (e *turnEngine) reportDiagnosticIfSet(ctx actor.Context, source, message, turnID string) {
	if e.onReportDiagnostic != nil {
		e.onReportDiagnostic(ctx, domain.OracleReportDiagnosticReq{
			Severity: "error",
			Source:   source,
			Message:  message,
			TurnID:   turnID,
			Unit:     modelUnitPtrToOracle(e.startReq.Input.Unit),
		})
	}
}
