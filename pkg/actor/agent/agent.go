package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/gospore/resource"

	"github.com/qomos-w/sporemind/pkg/actor/agent/memory"
	"github.com/qomos-w/sporemind/pkg/agentkit"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"

	"github.com/qomos-w/sporemind/pkg/llmclient"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/runtime"
	"github.com/qomos-w/sporemind/pkg/tokenest"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// coordEventLoop is the dedicated stateful lane for inbound event callbacks
// pushed at the agent by other actors (coordinator glass intake, lifecycle /
// status / tools-refresh notifications, worktree binding refresh). It is
// declared in OnStart via RegisterLoop and selected per-callable with
// actor.WithLoop(coordEventLoop). See the registration comment in OnStart.
const coordEventLoop = "coord_event"

// agent 包实现了一个 gospore actor，作为用户与 LLM 之间的交互代理。
// 它把聊天消息转化为一次或多次 turn，并在每个 turn 内驱动四阶段状态机：
// Dispatch → Audit → Execute → Commit。子任务可通过 fork_child 拆分为
// 只读探索子 agent，结果通过 explore_progress / explore_complete 回流。
//
// 总体流程图：
//
//	┌──────────────────────────────┐
//	│     用户消息 chat.submit     │
//	└───────────┬──────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│     startTurn 组装上下文     │
//	└───────────┬──────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│   handleRun → turnEngine     │
//	└───────────┬──────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│ Dispatch Audit Execute Commit │
//	└───────────┬──────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│  handleTurnComplete 持久化   │
//	└──────────────────────────────┘
//
//	支线 — fork_child：
//	┌──────────────────────────────┐
//	│   fork_child 孵化子 agent    │
//	└───────────┬──────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│    explore_progress 回流     │
//	└───────────┬──────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│    explore_complete 结束     │
//	└──────────────────────────────┘

// sessionSnapshotData is a point-in-time copy of all actor fields needed
// by the pure session.summary handler. Updated atomically via takeSnapshot()
// after each stateful mutation on the cell goroutine — no locks needed.

type sessionSnapshotData struct {
	ready           bool
	turns           []domain.Turn
	activeHead      int32
	activeTurnRef   string
	turnID          string
	turnStatus      domain.TurnStatus
	statusState     string
	statusPauseKind string
	exploreResults  []domain.ExploreResult
	tasks           []gen.TurnTask
	steps           []domain.Step
	summarySegments []domain.SummarySegment
	goal            *gen.SessionGoal
	nextIdx         int32
	nextSeq         int64
	// mailbox is a snapshot copy of a.mailbox for the pure agent.message.list
	// handler, so it can run off the owner queue without racing mailbox writes.
	mailbox []domain.AgentMessage
	// messages is the flat chat-history list (steps + active turn delta) for
	// the pure agent.messages.list handler, published by takeSnapshot() from
	// the lazily-converted msgCache (dirty slots re-converted per publish).
	messages []domain.ChatMessage
	// openStepEvents holds per-open-step event sequences from the active turn
	// engine. Returned on reconnect so the frontend can replay deltas for steps
	// that are still in flight.
	openStepEvents map[string][]domain.StepEvent
}

// Actor is a per-instance chat actor bound to one aiaggregator child.

type Actor struct {
	actor.Host

	// snapshot holds a point-in-time copy of session/turn state for the pure
	// (stateless) agent.session.summary handler. Updated atomically via
	// takeSnapshot() after each stateful mutation — lock-free concurrent reads.
	snapshot atomic.Pointer[sessionSnapshotData]

	Session           domain.Session               `gospore:"component,public"`
	RawSession        domain.RawSession            `gospore:"component,public"`
	ComponentMounts   []domain.AgentComponentMount `gospore:"component,public"`
	ComponentRevision int32                        `gospore:"component,public"`
	cardRefs          []gen.CardRef
	turnOrderByID     map[string]int64
	activeTurnOrder   int64
	// turnRefMu serializes writes to ActiveTurnRef and activeTurnOrder. Reads
	// from the turnEngine flush goroutine (takeSnapshot) hold this lock so they
	// never race with owner-loop mutations.
	turnRefMu sync.RWMutex
	// pendingSkillStepID holds the slash-skill step ID that must be emitted
	// inside the assistant turn (after turn.started) instead of inside the user
	// turn. The step is created in handleChatSubmit but its events are deferred
	// to startTurnWithName so they are ordered after turn.started.
	pendingSkillStepID string
	ActiveTurnRef      string `gospore:"component,public"`
	DisplayName        string `gospore:"component,public"`
	TurnState          string `gospore:"component,public"`
	TurnPauseKind      string `gospore:"component,public"`
	topo               runtime.TopologyProvider
	agentKind          string
	// Coordinator-only runtime record of delivered glass inbox events
	// (Stage 5). Bounded, never persisted; see coordinator_glass_event.go.
	coordGlassEventsMu sync.Mutex
	coordGlassEvents   []gen.GlassEventDeliverReq
	// guidanceStore holds coordinator-owned, per-account onboarding state.
	guidanceStore persist.Persist
	guidanceMu    sync.RWMutex
	// Coordinator-only runtime record of pending wearable interactions
	// (Stage 4 high-level notify/call tools). Bounded, never persisted;
	// see coordinator_wearable.go.
	wearableInteractionsMu sync.Mutex
	wearableInteractions   []coordWearableInteraction
	// slotMu protects the model slot fields below across the owner loop
	// (agent_configure writes) and stateless handlers (resolve_child_slot
	// reads from a forked goroutine).
	slotMu    sync.RWMutex
	primary   domain.ModelSlot
	fast      domain.ModelSlot
	execution domain.ModelSlot
	review    domain.ModelSlot
	summary   domain.ModelSlot

	// aggRefCache maps aggregator config ID ("system" or a named id) to its
	// resolved actor ref. Lazily refreshed from aimanager via topo discovery.
	// aggRefRefreshAt bounds the refresh rate: a cache miss whose refresh
	// succeeds but leaves the requested id unlisted (unspawned or dead
	// aggregator) must not re-invoke aimanager.aggregator_list on every
	// dispatch attempt.
	aggRefMu        sync.RWMutex
	aggRefCache     map[string]ref.Ref
	aggRefRefreshAt time.Time

	// ctxWindowWarned dedupes the context-window resolution WARN in
	// resolveUnitMaxContextLength (key = "model|provider"). Once a unit has
	// drifted from the aggregator pool the condition persists for the agent's
	// lifetime; repeating the warning per turn/dispatch only floods the log.
	ctxWindowWarned sync.Map

	// pendingChangeUnit is staged by handleConfigure when the user changes
	// PrimaryUnit while a turn is running. The active turnEngine consumes it
	// at the next safe judgment window (after phaseCommit) and uses the new
	// unit for the next LLM dispatch iteration.
	pendingChangeUnit atomic.Pointer[domain.ModelUnit]

	// pendingToolsRefresh is staged by the component mount/unmount/set_enabled
	// handlers when the component snapshot changes while a turn is running.
	// The active turnEngine consumes it at the next safe judgment window and
	// re-resolves its tool surface (baseTools + rebuildToolsForModel) so tools
	// contributed by a bundle mounted mid-turn become visible and resolvable
	// for the remaining LLM dispatches of the same turn.
	pendingToolsRefresh atomic.Bool

	// permissionMode controls the global tool-permission strategy.
	// "permission" (default): user confirms each mutating tool call.
	// "allow-all": no confirmation — all tool calls are auto-approved;
	// plan_submit / goal_submit still wait for user approval and ask_user is
	// still allowed.
	// "yolo": allow-all + plan_submit/goal_submit are auto-approved and
	// ask_user is blocked — the agent acts fully autonomously, never asking
	// the user.
	// "auto": the fast model auto-decides tool calls via a single non-context
	// call; plan_submit/goal_submit still wait for user approval and ask_user
	// is still allowed.
	// "autopilot": auto-style tool guard (fast model decides) + yolo-style
	// auto-approval of plan_submit/goal_submit/workflow_start/goal_card_submit
	// and ask_user is blocked — the agent acts fully autonomously but every
	// tool call is vetted by the fast model.
	permissionMode string
	// permissionModeOverride marks that permissionMode was explicitly set via
	// permission_mode_set (a per-agent override) rather than inherited from the
	// global preference at spawn time. Only overrides are persisted, so a
	// restart keeps the user's per-agent choice while a non-overridden agent
	// keeps re-reading the (possibly changed) global default at spawn.
	permissionModeOverride bool

	steps     []domain.Step
	stepsGen  int64
	stepsSync struct {
		gen int64
		len int
	}
	// msgCache is the lazily-maintained ChatMessage projection of a.steps:
	// the invariant len(msgCache) == len(a.steps) always holds. Entries are
	// produced by stepToChatMessage at appendStep / rebuildMsgCache time and
	// are immutable once generated — a changed step is re-converted into its
	// slot, never mutated in place, so snapshots published via a private
	// slice copy never observe slot replacement. msgDirty marks slots whose
	// step changed since the cached entry was built; takeSnapshot rebuilds
	// only dirty slots before splicing, dropping message-build cost from
	// O(total content) to O(dirty steps).
	msgCache []domain.ChatMessage
	msgDirty []bool
	// stepsPersistedMu guards stepsPersisted. The map is touched both from the
	// system loop (OnStart → saveMailbox → flushClosedSteps) and the owner loop
	// (import handlers → saveMailbox → flushClosedSteps); without a lock a clone
	// whose import runs concurrently with OnStart hits a fatal concurrent
	// map read/write.
	//
	// Serialization invariant: stepsPersistedMu also serializes append-order
	// for per-turn step JSONL files. flushClosedSteps holds the lock while
	// checking stepsPersisted and calling persistStepToFile, so steps for the
	// same turn are appended in Seq order. The per-path lock inside the
	// persist Appender prevents interleaving between concurrent Append calls,
	// but only stepsPersistedMu guarantees the *logical* order (step N is
	// appended before step N+1). Without this mutex, two goroutines flushing
	// steps from the same turn could interleave their appends, producing a
	// JSONL file whose lines are out of order.
	stepsPersistedMu sync.Mutex
	stepsPersisted   map[string]struct{}

	// requestRecords is the session-level request ledger, rebuilt from
	// requests/ledger.jsonl on load and appended to during each turn completion
	// and compaction. requestRecordsPersisted tracks how many records have been
	// flushed to disk; new records are written during saveMailbox.
	requestRecords          []domain.SessionRequestRecord
	requestRecordsPersisted int

	// turnSync tracks the last persisted JSON signature of each turn so
	// saveTurns only rewrites turns whose content actually changed. Without
	// this, turns reached a terminal state (running → completed/failed) but
	// the .json file was never updated — restart showed stale state (AUDIT 4.1).
	turnSync map[string]string

	// Inline turn execution. Access via activeTurnEngine() / turnEngineStore()
	// to avoid races between the default loop and agent.exec.
	turnEngine atomic.Pointer[turnEngine]

	// plan holds the plan-mode state that persists across turns until the
	// user resolves (approves / rejects) a pending plan_submit.
	plan planState

	// Mailbox stores messages received from other agents.
	mailbox []domain.AgentMessage

	// pendingSubmits stores per-turn pending text submissions (plan mode).
	// Keyed by TurnID. Moved from Turn.PendingSubmits after flattening.
	pendingSubmits map[string][]domain.PendingSubmit

	// cfg holds runtime configuration overrides: compaction policy, token
	// estimation calibration, and thinking-level registry. These are mutable
	// across the actor lifetime via configure callables.
	cfg runtimeConfig

	// projectApprovals records callable IDs that have been approved for a
	// specific project. Key format: "projectID|callableID".
	projectApprovals map[string]struct{}

	// usedSkills tracks skill IDs invoked via agent.skill.use in the current
	// session so duplicate calls can return a warning while refreshing the body.
	// Guarded by usedSkillsMu: skill_use runs on the agent_exec loop lane while
	// the slash path (synthesizeSkillMount) writes from turn lanes.
	usedSkills   map[string]struct{}
	usedSkillsMu sync.Mutex

	actorID       string // own canonical ID for persistence key
	workspaceID   string // parent workspace actor id for aistats routing
	parentAgentID string // parent agent actor id when spawned by another agent (spawn_assign); empty for user-created root agents

	// extraBundleIDs holds spawn-time bundle card IDs (beyond the kind's
	// DefaultBundleIDs) auto-mounted on this agent — e.g. the plugin-dev
	// bundle attached when the agent is spawned in a dev-app project. Set at
	// construction; the mounts themselves persist via ComponentMounts.
	extraBundleIDs []string

	// Bound app (plugin) fields, set via agent_bind_app for agents provisioned
	// by an app registration (plugin_agent block). (BoundAppID, the slot the
	// workspace allocated for this binding) is the key in the workspace agent
	// registry — the actor itself only needs the app id: BoundAppPrompt
	// overlays the builtin plugin-agent profile prompt in the component
	// snapshot; boundBundleCardIDs is the last reconciled desired bundle card
	// list.
	boundAppMu         sync.RWMutex
	boundAppID         string
	boundAppPrompt     string
	boundBundleCardIDs []string

	// aistatsRef is lazily resolved from workspace.aistats_actor_id.
	aistatsRef     ref.Ref
	aistatsActorID string

	// costRates caches cost rates from the aistats actor so per-request cost
	// can be computed without querying aistats on every dispatch.
	costMu      sync.RWMutex
	costRates   map[costRateKey]llmclient.CostRate
	costExpires time.Time

	// status is the derived snapshot of the currently executing (or most
	// recently completed) turn. Updated by resetTurnSnapshot and turn lifecycle
	// handlers; read lock-free via buildTurnStatusReadOnly / snapshot.
	status turnStatus

	// child holds all state for forked sub-agent (explorer) mode.
	// When child.Mode is true, this agent registers only read-only tools,
	// auto-starts an exploration turn, and forwards progress + results to
	// the parent via agent.explore_progress / agent.explore_complete.
	child childState

	// clone bookkeeping for async agent cloning. Only populated when this
	// agent is the target of a chunked clone. In-memory only; lost on restart.
	cloneSourceActorID    string
	cloneSourceTotalTurns int
	clonePendingHistory   bool
	cloneSourceSet        bool

	// Active child agents, tracked so we can cancel them when the parent
	// turn is cancelled. Keyed by toolUseID for parallel fork support.
	activeChildren   map[string]ref.Ref
	activeChildrenMu sync.Mutex

	// childNameSeq generates unique child actor names without calling
	// ctx.NewID() from goroutines (it is not thread-safe).
	childNameSeq atomic.Uint64

	// exploreProgressSnapshotAt throttles the atomic session snapshot taken on
	// child explore_progress (see snapshotAfterChildProgress).
	exploreProgressSnapshotAt time.Time

	// UI-facing aggregated state pushed to workspace.agent_list_state.
	worktreeID          string
	worktreeName        string
	worktreeStatus      string
	worktreePath        string
	pendingApproval     bool
	planApprovalPending bool
	pendingAskUser      bool
	activeThinkLevel    string

	// goal_submit interaction state
	goalSubmitPending   bool
	goalSubmitRequestID string

	// goal_card_submit interaction state
	goalCardSubmitPending   bool
	goalCardSubmitRequestID string
	// goalCardProposal is the pending task-card proposal awaiting user
	// confirmation. Restored after restart from the persisted pending
	// interaction Task payload (setPendingInteraction + saveMailbox).
	goalCardProposal *goalCardProposalState

	// workflow_start interaction state (two-phase confirmation before activating a
	// workflow map). Mirrors goal_submit but stores the derived goal in
	// RawSession.PendingWorkflowStart so it survives restart.
	workflowStartPending   bool
	workflowStartRequestID string

	// pendingInteraction records the one blocking interaction currently awaiting
	// a user answer (ask_user / ask_permission / plan_approval / goal_submit /
	// workflow_start). It is persisted as part of the mailbox snapshot so the
	// blocked state survives an agent restart: the in-memory turnEngine goroutine
	// that normally answers via resumeCh is lost on restart, but this record
	// lets OnStart restore the interaction flags and lets handleTurnAnswer apply
	// the answer when no live engine exists. At most one interaction blocks a
	// turn at a time.
	pendingInteraction *pendingInteractionState
	// pendingInteractionResolution holds the user's answer once it is applied on
	// the no-engine path, so the new turn started by handleTurnAnswer can consume
	// it as user input. Cleared once consumed.
	pendingInteractionResolution *pendingResolutionState
	// title holds the inferred title for the current task, generated by
	// the fast model when the caller does not supply an explicit title.
	title string

	// Title is the public projection component for the current task title.
	// It is kept in sync with the private title field by handleSetTitle and
	// loadMailbox so the actor inspector can read it without a synchronous call.
	Title string `gospore:"component,public"`

	// stepEventSeq is a monotonic counter for step events emitted outside of
	// turnEngine context (plan_approval, task_created, task_updated). The
	// frontend uses EventSeq for dedup and replay.
	stepEventSeq int32

	// snapshotReady is set once the actor has finished its initial load
	// (loadMailbox + rebuildSteps for normal agents, or NewChildActor setup
	// for child agents). It is mutated on the cell goroutine and copied into
	// every snapshot so that pure readers can tell whether the snapshot is
	// authoritative yet.
	snapshotReady bool

	// snapshotFlushedAt records (unix nanos) when takeSnapshot last rebuilt
	// the atomic snapshot. The streaming path (flushDeltas → flushEvents →
	// onAfterFlush) reads it to rate-limit O(session)-sized rebuilds: with
	// hundreds of chunks per LLM response each rebuilding a full copy, the
	// recv loop's consume rate falls behind the producer and the invoke
	// layer evicts the caller ("caller stalled (buffer full), stream
	// evicted" — 2026-08-27 incident). Atomic because the streaming-path
	// reader may be the planner.Stream promise goroutine, not the owner
	// lane. Boundary callers use takeSnapshot directly and always rebuild.
	snapshotFlushedAt atomic.Int64

	// onStartDone is closed when OnStart finishes (success, error, or panic),
	// via the deferred markStarted() at the top of OnStart. The pure
	// session.summary handler waits on it to bridge cold start without blocking
	// the owner loop — OnStart synchronously calls back into the workspace
	// owner loop (fetchAgentKindConfig), so only an off-loop (pure) waiter is
	// safe. NEVER wait on this from the workspace/project owner loops.
	onStartDone chan struct{}

	// kindConfig caches the agent kind configuration fetched from workspace.
	// fetchAgentKindConfig is called 3+ times per turn start; caching avoids
	// that many synchronous cross-actor calls that block the owner loop.
	kindConfig atomic.Pointer[domain.AgentKindConfig]

	// pendingSpawnGoal carries goal data threaded through the spawn chain
	// (ProjectSpawnAgentReq → NewActor). When non-nil, OnStart self-assigns
	// the goal after all handlers are registered, avoiding the race where a
	// cross-actor assign_goal invoke arrives before handler registration.
	pendingSpawnGoal *domain.AgentInternalAssignGoalReq

	// resolvedInstructions caches the assembled prompt and environment
	// contributions for the current component revision.

	resolvedInstructions atomic.Pointer[domain.CompiledInstructions]
	componentSnapshot    atomic.Pointer[domain.AgentComponentSnapshot]
	cachedProjectRoot    atomic.Pointer[string]

	// Graph is the in-memory memory graph, nil when builtin:mode:memory is
	// not mounted. Created during syncMemoryMountState and torn down on unmount.
	// graphMu guards the Graph pointer (mount/unmount write it; pure handlers
	// and the exec loop read it from other goroutines). The MemoryGraph itself
	// is internally locked, so callers only need the read lock to obtain a
	// stable pointer via graphRead.
	Graph   *memory.MemoryGraph
	graphMu sync.RWMutex

	// memoryPersisted holds the serialized graph snapshot bytes from the
	// previous life. Loaded by loadMailbox and restored by syncMemoryMountState.
	memoryPersisted json.RawMessage
	memorySleeping  bool
	// memorySleepTurn records the turn ID that already spawned a memory sleep;
	// prevents a second spawn from later compactions within the same turn.
	memorySleepTurn string
	// sleepFailureCount tracks consecutive ApplySleep failures. When >= 3,
	// automatic sleep is skipped (force=true still triggers) to prevent
	// infinite retry loops. Reset on successful ApplySleep and on graph
	// mutations (AddNode, AddEdge).
	sleepFailureCount int
	// dreamStepID tracks the assistant step opened for a /dream command so
	// finishMemorySleep can emit results and close it.
	dreamStepID string

	// thinkingRegistry holds the per-model-unit thinking-level overrides as an
	// atomic snapshot so the pure agent.thinking.get_registry handler can read
	// them off the owner queue. Mutated via storeThinkingLevel / loadThinkingRegistry.
	thinkingRegistry atomic.Pointer[map[thinkingUnitKey]gen.ThinkingLevel]

	// cachedPromptArtifact / cachedCompiledPrompt hold the most recently built
	// prompt artifacts, refreshed on the cell goroutine (OnStart + after each
	// turn compile) so the pure agent.prompt_artifact / agent.compiled_prompt
	// handlers can return them without entering the owner queue.
	cachedPromptArtifact atomic.Pointer[domain.PromptArtifact]
	cachedCompiledPrompt atomic.Pointer[domain.PromptArtifact]

	// statusNotifyLatest holds the newest workspace status update awaiting
	// delivery; statusNotifyInflight CAS-gates a single sender goroutine so
	// updates arrive in order and the latest state always wins.
	// notifyWorkspaceStatus is called from both the cell goroutine and
	// turnEngine goroutines; spawning independent goroutines per update let a
	// stale "paused" update land after a newer "completed" update, leaving the
	// avatar stuck on the pause icon even though the turn succeeded.
	statusNotifyLatest   atomic.Pointer[gen.WorkspaceAgentStatusUpdateReq]
	statusNotifyInflight atomic.Bool
	statusNotifyDone     atomic.Pointer[chan struct{}]

	// routeReportPending arms a one-shot Primary inclusion in the next
	// workspace status update. It is set only when the agent's primary route
	// chain actually changes (record/demote), so ordinary status ticks (and the
	// slot changes pushed down by agent.configure) never carry a Primary and
	// cannot clobber a user's freshly configured slot during the async
	// reconfigure window. See notifyWorkspaceStatus / handleAgentStatusUpdate.
	routeReportPending atomic.Bool
}

// turnStatus aggregates all derived fields describing the current turn's
// observable state: model, usage, step index, and running status.
type turnStatus struct {
	State          string
	PauseKind      string
	Unit           domain.ModelUnit
	TurnID         string
	ActiveStepID   string
	Usage          *domain.UsageData
	ContextBudget  *domain.TurnContextBudgetPayload
	StartStepCount int
	StartedAt      string
	StepByID       map[string]domain.TurnAction
	StepOrder      []string
}

// planState holds the plan-mode runtime that persists across turns.
type planState struct {
	Task          string
	Status        string // idle | pending_approval | approved | rejected | completed
	Title         string
	Plan          string
	PlanCardID    string // ID of the persisted plan wiki card
	Policy        *gen.PlanPolicy
	RequestID     string
	PendingTasks  []gen.PlanTaskRef `json:"pendingTasks,omitempty"`
	ApprovedTasks []gen.Task        `json:"approvedTasks,omitempty"`
}

// pendingInteractionState mirrors a persisted step.interaction_requested: just
// enough to restore the blocked state after restart. One per turn.
type pendingInteractionState struct {
	TurnID    string `json:"turnId,omitempty"`
	StepID    string `json:"stepId,omitempty"`
	RequestID string `json:"requestId,omitempty"`
	// Type is the InteractionType: ask_user / permission / plan_approval /
	// goal_submit / goal_card_submit.
	Type string         `json:"type,omitempty"`
	Task map[string]any `json:"task,omitempty"`
}

// pendingResolutionState carries the user's answer to a resolved blocking
// interaction so a restart-spawned turn can consume it as user input.
type pendingResolutionState struct {
	RequestID   string `json:"requestId,omitempty"`
	AnswersJSON string `json:"answersJson,omitempty"`
}

// runtimeConfig aggregates runtime overrides and calibration state that is
// mutated by configure callables during the actor lifetime.
type runtimeConfig struct {
	CompactionOverride *domain.CompactionPolicy
	TokenCalibration   *tokenest.Calibration
}

// childState aggregates all fields used when this agent is a short-lived
// forked sub-agent. Keeping them in one struct makes the child-mode data
// flow explicit and keeps the Actor struct flat.
type childState struct {
	Mode                     bool
	AgentRef                 ref.Ref
	TurnRef                  string
	StepRef                  string
	ToolUseID                string
	SearchCnt                int32
	ReadCnt                  int32
	InputTokens              int32
	OutputTokens             int32
	CacheCreationInputTokens int32
	CacheReadInputTokens     int32
	MaxIterations            int32
	ActiveWork               []gen.WorkItem

	// SummaryText accumulates block.delta text in child mode so it can
	// be forwarded to the parent via ForkChildProgress.SummaryText. Live
	// flushes send only a tail window (tailWindowSummary); the full summary
	// is delivered once via explore_complete.
	SummaryText string

	// Phase is the current explore phase (searching/reading) derived from
	// executed tools, forwarded to the parent via explore_progress.
	Phase string

	// LastProgressFlush tracks the last time an explore_progress was sent
	// to the parent. Used to rate-limit progress flushes.
	LastProgressFlush time.Time

	// ProgressDirty is set when block.delta or tool execution data has
	// changed since the last explore_progress flush.
	ProgressDirty bool

	// HotContext is pre-resolved context injected by the parent at fork time.
	// Child actors cannot resolve hot context themselves (their agentKind is
	// "explorer" and ctx.Parent() points to the parent agent, not the
	// project actor).
	HotContext []domain.ContentBlock

	// LastActivityNs is written by the child turn lifecycle and read by the
	// concurrent stateless agent_status handler.
	LastActivityNs atomic.Int64
}

// thinkingUnitKey is the registry key for per-unit thinking overrides. It is an
// explicit {Model, Provider} struct rather than domain.ModelUnit so that
// ModelUnit's optional ThinkLevel field never participates in key equality — a
// runtime override is keyed by model+provider identity alone, independent of
// the unit's own default think level.
type thinkingUnitKey struct {
	Model    string
	Provider string
}

// unit conversion helpers: ModelUnit is now generated once and shared across
// schemas; these helpers preserve the explicit crossing points and match the
// pointer/value shape of the target generated field.
func aigenUnitToAgent(u gen.ModelUnit) gen.ModelUnit {
	return gen.ModelUnit(u)
}

func aigenUnitPtrToAgent(u *gen.ModelUnit) gen.ModelUnit {
	if u == nil {
		return gen.ModelUnit{}
	}
	return *u
}

func agentUnitPtr(u domain.ModelUnit) *gen.ModelUnit {
	if u.Model == "" && u.Provider == "" {
		return nil
	}
	v := gen.ModelUnit(u)
	return &v
}

func modelUnitPtrToOracle(u *gen.ModelUnit) *gen.ModelUnit {
	if u == nil {
		return nil
	}
	v := gen.ModelUnit{Model: u.Model, Provider: u.Provider}
	return &v
}

// modelUnitEqual compares two ModelUnit values by their identifying fields.
func modelUnitEqual(a, b domain.ModelUnit) bool {
	return a.Model == b.Model && a.Provider == b.Provider
}

func (a *Actor) Type() string { return "agent" }

// OnStart 是 agent actor 的生命周期入口。
// 负责三件事：
//   1. 绑定 aiaggregator（运行时 LLM 聚合器）；
//   2. 注册事件类型与所有 callable（常规 agent 完整 callable 面，子 agent 最小面）；
//   3. 恢复/重建状态并初始化快照。
//
// 常规 agent 会注册 agent.exec 自定义 loop，并把 agent.run 绑定到该 loop，
// 这样 handleRun 内部创建的 turnEngine 可以在这个 loop 上安全运行。
//
// 流程图：
//
//	┌──────────────────────────────┐
//	│  解析 aggregator ID 并绑定   │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│   重建 steps 并初始化快照    │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│   注册 turn/step 事件类型    │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│         child.Mode ?         │
//	└──────────────────────────────┘
//	            ↓
//	       是 /     \ 否
//	         /       \
//	 onStartChild    注册完整 callable 面
//	                  + agent.exec loop

func (a *Actor) OnStart(ctx actor.Context) error {
	// markStarted closes onStartDone on every exit path (success, error, or
	// panic), releasing pure session.summary calls that arrived during cold
	// start. Must be the first statement.
	defer a.markStarted()
	ctx.Logger().Info("agent: starting", "id", ctx.Self().ID().String(), "kind", a.agentKind)

	// Status notifications are sent lazily by notifyWorkspaceStatus: the first
	// update after OnStart CAS-arms the single-flight sender goroutine.

	if prov, ok := resource.Get[runtime.TopologyProvider](ctx.Resources(), runtime.TopologyKey); ok {
		a.topo = prov
	}
	// Seed the atomic snapshot so the pure session.summary handler has data
	// immediately — takeSnapshot must run on the cell goroutine.
	// Child agents seed steps from NewChildActor and have no Session.Turns yet.
	// AUDIT 5.2: prior impl also called rebuildSteps here, before loadMailbox
	// ran. The result was clobbered by the post-loadMailbox call at line 513,
	// and any steps loaded between the two calls were snapshot before rebuild.
	if !a.child.Mode {
		a.initNextSeq()
		a.reconcilePersistedOrphans()
	}
	// Child agents are fully initialized by NewChildActor; normal agents are
	// not ready until loadMailbox + rebuildSteps has produced the final
	// snapshot below. Use the flag so pure session.summary doesn't return
	// an empty/partial seed snapshot during cold start.
	a.snapshotReady = a.child.Mode
	a.takeSnapshot()

	if err := ctx.RegisterEventKind("turn", domain.TurnEvent{}, actor.Public()); err != nil {
		return fmt.Errorf("agent: register turn event kind: %w", err)
	}
	if err := ctx.RegisterEventKind("step", domain.StepEvent{}, actor.Public()); err != nil {
		return fmt.Errorf("agent: register step event kind: %w", err)
	}

	// onStartDone (closed by the deferred markStarted below) is consumed by the
	// pure session.summary handler to wait out cold start WITHOUT blocking the
	// owner loop — see handleSessionSummaryPure. It must not be probed from the
	// workspace/project owner loops, because OnStart synchronously calls back
	// into those loops (fetchAgentKindConfig), which would deadlock.

	if a.child.Mode {
		return a.onStartChild(ctx)
	}

	// Domain declaration: "agent" namespace for local callables (not
	// exposed as a service; routing falls back to ctx.Self()).
	ctx.RegisterDomain("agent")

	// Exec loop for inline turn dispatch.
	if err := ctx.RegisterLoop("agent_exec", actor.ModeStateful); err != nil {
		return fmt.Errorf("agent: register exec loop: %w", err)
	}
	if err := ctx.Register("agent_run", a.handleRun, actor.Internal(), actor.WithLoop("agent_exec")); err != nil {
		return fmt.Errorf("agent: register agent_run: %w", err)
	}
	// Coord-event loop for state-writing callbacks pushed at the agent from
	// other actors (coordinator glass intake, lifecycle/status/tools-refresh
	// notifications, worktree binding refresh). These are stateful (they
	// mutate session/binding state) but must not serialize behind the owner
	// lane's business ingress: a burst of inbound event callbacks would
	// otherwise head-of-line block submit/cancel/status reads. Routing them
	// onto their own stateful lane keeps the owner lane free while preserving
	// single-goroutine serialization among the callbacks themselves.
	if err := ctx.RegisterLoop(coordEventLoop, actor.ModeStateful); err != nil {
		return fmt.Errorf("agent: register coord_event loop: %w", err)
	}
	// Stats records are emitted as events (aistats.record) rather than
	// invokes: no PendingTable slot, no aistats owner-loop serialization,
	// no per-call goroutine. The event bus provides drop-oldest backpressure.
	if err := ctx.RegisterEventKind("aistats.record", domain.AIStatsRecord{}, actor.Internal()); err != nil {
		return fmt.Errorf("agent: register event kind aistats.record: %w", err)
	}
	if err := ctx.Register("internal_start_turn", a.handleStartTurnInternal, actor.Internal()); err != nil {
		return fmt.Errorf("agent: register internal_start_turn: %w", err)
	}
	if err := ctx.Register("internal_assign_goal", a.handleInternalAssignGoal, actor.Internal()); err != nil {
		return fmt.Errorf("agent: register internal_assign_goal: %w", err)
	}
	if err := ctx.Register("internal_resume_from_review", a.handleInternalResumeFromReview, actor.Internal()); err != nil {
		return fmt.Errorf("agent: register internal_resume_from_review: %w", err)
	}

	// Normal agent: full callable surface.
	if err := ctx.Register("chat_submit", a.handleChatSubmit, actor.Public()); err != nil {
		return fmt.Errorf("agent: register chat_submit: %w", err)
	}
	if a.agentKind == domain.AgentKindCoordinator {
		a.initCoordinatorGuidance()
		if err := ctx.Register("coordinator_guidance_profile_query", a.handleCoordinatorGuidanceQuery, actor.Public()); err != nil {
			return fmt.Errorf("agent: register coordinator guidance query: %w", err)
		}
		if err := ctx.Register("coordinator_guidance_profile_update", a.handleCoordinatorGuidanceUpdate, actor.Public()); err != nil {
			return fmt.Errorf("agent: register coordinator guidance update: %w", err)
		}
		if err := ctx.Register("coordinator_guidance_profile_clear", a.handleCoordinatorGuidanceClear, actor.Public()); err != nil {
			return fmt.Errorf("agent: register coordinator guidance clear: %w", err)
		}
		if err := ctx.Register("coordinator_guidance_profile_increment", a.handleCoordinatorGuidanceIncrement, actor.Public()); err != nil {
			return fmt.Errorf("agent: register coordinator guidance increment: %w", err)
		}
		if err := ctx.Register("coordinator_ingest_glass_transcript", a.handleCoordinatorGlassTranscript, actor.Internal(), actor.WithLoop(coordEventLoop)); err != nil {
			return fmt.Errorf("agent: register coordinator_ingest_glass_transcript: %w", err)
		}
		if err := ctx.Register(coordinatorGlassEventIntake, a.handleCoordinatorGlassEvent, actor.Internal(), actor.WithLoop(coordEventLoop)); err != nil {
			return fmt.Errorf("agent: register coordinator_ingest_glass_event: %w", err)
		}
		if err := ctx.Register(coordinatorGlassReplyIntake, a.handleCoordinatorGlassReply, actor.Internal(), actor.WithLoop(coordEventLoop)); err != nil {
			return fmt.Errorf("agent: register coordinator_ingest_glass_reply: %w", err)
		}
		if err := ctx.Register(coordinatorLifecycleNotify, a.handleCoordinatorLifecycleNotify, actor.Internal(), actor.WithLoop(coordEventLoop)); err != nil {
			return fmt.Errorf("agent: register coordinator_lifecycle_notify: %w", err)
		}
		if err := ctx.Register(coordinatorWearableNotifyCallable, a.handleCoordinatorWearableNotify, actor.Internal()); err != nil {
			return fmt.Errorf("agent: register coordinator_wearable_notify: %w", err)
		}
		if err := ctx.Register(coordinatorWearableCallCallable, a.handleCoordinatorWearableCall, actor.Internal()); err != nil {
			return fmt.Errorf("agent: register coordinator_wearable_call: %w", err)
		}
	}
	if err := ctx.Register("chat_cancel_pending", a.handleCancelPendingSubmit, actor.Public()); err != nil {
		return fmt.Errorf("agent: register chat_cancel_pending: %w", err)
	}
	if err := ctx.Register("turn_cancel", a.handleTurnCancel, actor.Public()); err != nil {
		return fmt.Errorf("agent: register turn_cancel: %w", err)
	}
	if err := ctx.Register("turn_pause", a.handleTurnPause, actor.Public()); err != nil {
		return fmt.Errorf("agent: register turn_pause: %w", err)
	}
	if err := ctx.Register("turn_resume", a.handleTurnResume, actor.Public()); err != nil {
		return fmt.Errorf("agent: register turn_resume: %w", err)
	}
	if err := ctx.Register("agent_pause", a.handleAgentPause, actor.Public()); err != nil {
		return fmt.Errorf("agent: register agent_pause: %w", err)
	}
	if err := ctx.Register("agent_resume", a.handleAgentResume, actor.Public()); err != nil {
		return fmt.Errorf("agent: register agent_resume: %w", err)
	}
	if err := ctx.Register("turn_answer", a.handleTurnAnswer, actor.Public()); err != nil {
		return fmt.Errorf("agent: register turn_answer: %w", err)
	}
	if err := ctx.Register("turn_complete", a.handleTurnComplete, actor.Internal()); err != nil {
		return fmt.Errorf("agent: register turn_complete: %w", err)
	}
	if err := ctx.Register("permission_review", a.handlePermissionReview, actor.Internal()); err != nil {
		return fmt.Errorf("agent: register permission_review: %w", err)
	}
	if err := ctx.Register("permission_mode_set", a.handleSetPermissionMode, actor.Public()); err != nil {
		return fmt.Errorf("agent: register permission_mode_set: %w", err)
	}
	if err := ctx.Register("session_undo", a.handleSessionUndo, actor.Public()); err != nil {
		return fmt.Errorf("agent: register session_undo: %w", err)
	}
	if err := ctx.Register("session_fork", a.handleSessionFork, actor.Public()); err != nil {
		return fmt.Errorf("agent: register session_fork: %w", err)
	}
	if err := ctx.Register("session_import", a.handleSessionImport, actor.AdminOnly()); err != nil {
		return fmt.Errorf("agent: register session_import: %w", err)
	}
	if err := ctx.Register("session_export_range", a.handleSessionExportRange, actor.AdminOnly()); err != nil {
		return fmt.Errorf("agent: register session_export_range: %w", err)
	}
	if err := ctx.Register("session_import_turns", a.handleSessionImportTurns, actor.AdminOnly()); err != nil {
		return fmt.Errorf("agent: register session_import_turns: %w", err)
	}
	if err := ctx.Register("session_get", a.handleGetSession, actor.Public()); err != nil {
		return fmt.Errorf("agent: register session_get: %w", err)
	}
	if err := ctx.Register("session_summary", a.handleSessionSummaryPure, actor.Public()); err != nil {
		return fmt.Errorf("agent: register session_summary: %w", err)
	}
	if err := ctx.Register("session_stats", a.handleSessionStats, actor.Public()); err != nil {
		return fmt.Errorf("agent: register session_stats: %w", err)
	}
	if err := ctx.Register("turns_list", a.handleTurnsList, actor.Public()); err != nil {
		return fmt.Errorf("agent: register turns_list: %w", err)
	}
	if err := ctx.Register("inspect_pages", a.handleInspectPages, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
	); err != nil {
		return fmt.Errorf("agent: register inspect_pages: %w", err)
	}
	if err := ctx.Register("list_callables", a.handleListCallables, actor.Public()); err != nil {
		return fmt.Errorf("agent: register list_callables: %w", err)
	}
	if err := ctx.Register("invoke_callable", a.handleInvokeCallable, actor.Public()); err != nil {
		return fmt.Errorf("agent: register invoke_callable: %w", err)
	}
	if err := ctx.Register(internalRefreshWorktreeStatus, a.handleInternalRefreshWorktreeStatus, actor.Internal(), actor.WithLoop(coordEventLoop)); err != nil {
		return fmt.Errorf("agent: register %s: %w", internalRefreshWorktreeStatus, err)
	}
	if err := ctx.Register("frontend_debug", a.handleFrontendDebug, actor.Public()); err != nil {
		return fmt.Errorf("agent: register frontend_debug: %w", err)
	}
	if err := ctx.Register("inspect_actor", a.handleInspectActor, actor.Public()); err != nil {
		return fmt.Errorf("agent: register inspect_actor: %w", err)
	}
	if err := ctx.Register("capture_profile", a.handleCaptureProfile, actor.Public()); err != nil {
		return fmt.Errorf("agent: register capture_profile: %w", err)
	}
	if err := ctx.Register("agent_status", a.handleStatus, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
	); err != nil {
		return fmt.Errorf("agent: register agent_status: %w", err)
	}
	if err := ctx.Register("component_mount", a.handleComponentMount, actor.Public(),
		actor.WithEffect(string(domain.EffectReversible)),
	); err != nil {
		return fmt.Errorf("agent: register component_mount: %w", err)
	}
	if err := ctx.Register("component_unmount", a.handleComponentUnmount, actor.Public(),
		actor.WithEffect(string(domain.EffectReversible)),
	); err != nil {
		return fmt.Errorf("agent: register component_unmount: %w", err)
	}
	if err := ctx.Register("component_set_enabled", a.handleComponentSetEnabled, actor.Public(),
		actor.WithEffect(string(domain.EffectReversible)),
	); err != nil {
		return fmt.Errorf("agent: register component_set_enabled: %w", err)
	}
	if err := ctx.Register("component_list", a.handleComponentList, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
	); err != nil {
		return fmt.Errorf("agent: register component_list: %w", err)
	}
	if err := ctx.Register("component_snapshot", a.handleComponentSnapshot, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
	); err != nil {
		return fmt.Errorf("agent: register component_snapshot: %w", err)
	}
	if err := ctx.Register("modes_unload_all", a.handleModesUnloadAll, actor.Public(),
		actor.WithEffect(string(domain.EffectReversible)),
	); err != nil {
		return fmt.Errorf("agent: register modes_unload_all: %w", err)
	}
	if err := ctx.Register("scheduler_bind", a.handleSchedulerBind, actor.Public(),
		actor.WithEffect(string(domain.EffectReversible)),
	); err != nil {
		return fmt.Errorf("agent: register scheduler_bind: %w", err)
	}
	if err := ctx.Register("scheduler_unbind", a.handleSchedulerUnbind, actor.Public(),
		actor.WithEffect(string(domain.EffectReversible)),
	); err != nil {
		return fmt.Errorf("agent: register scheduler_unbind: %w", err)
	}
	if err := ctx.Register("tools_refresh_notify", a.handleToolsRefreshNotify, actor.AdminOnly(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithLoop(coordEventLoop),
	); err != nil {
		return fmt.Errorf("agent: register tools_refresh_notify: %w", err)
	}
	if err := ctx.Register("context_budget", a.handleContextBudget, actor.Public()); err != nil {
		return fmt.Errorf("agent: register context_budget: %w", err)
	}
	if err := ctx.Register("prompt_artifact", a.handlePromptArtifact, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
	); err != nil {
		return fmt.Errorf("agent: register prompt_artifact: %w", err)
	}
	if err := ctx.Register("compiled_prompt", a.handleCompiledPrompt, actor.Public()); err != nil {
		return fmt.Errorf("agent: register compiled_prompt: %w", err)
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
	if err := ctx.Register("read_snapshot", a.handleReadSnapshot, actor.Public()); err != nil {
		return fmt.Errorf("agent: register read_snapshot: %w", err)
	}
	if err := ctx.Register("resolve_child_slot", a.handleResolveChildSlot, actor.Internal()); err != nil {
		return fmt.Errorf("agent: register resolve_child_slot: %w", err)
	}
	if err := ctx.Register("image_generate", a.handleImageGenerate, actor.Internal()); err != nil {
		return fmt.Errorf("agent: register image_generate: %w", err)
	}
	if err := ctx.Register("image_recognize", a.handleImageRecognize, actor.Internal()); err != nil {
		return fmt.Errorf("agent: register image_recognize: %w", err)
	}
	if err := ctx.Register("video_generate", a.handleVideoGenerate, actor.Internal()); err != nil {
		return fmt.Errorf("agent: register video_generate: %w", err)
	}
	if err := ctx.Register("open_global_browser", a.handleOpenGlobalBrowser, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
	); err != nil {
		return fmt.Errorf("agent: register open_global_browser: %w", err)
	}
	if err := ctx.Register("show_page_thumbnail", a.handleShowPageThumbnail, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
	); err != nil {
		return fmt.Errorf("agent: register show_page_thumbnail: %w", err)
	}
	if err := ctx.Register("open_ssh_session", a.handleOpenSshSession, actor.Public(),
		actor.WithEffect(string(domain.EffectIrreversible)),
		actor.WithDescription("Open an interactive SSH shell session on a remote host. Before opening, check sshmanager.session_list for an existing session on the same host and reuse its sessionId with sshmanager.shell_run — do not open duplicate sessions. The desktop right panel opens a terminal tab bound to the session. Returns the session ID for shell_input/shell_stream calls. Only available in the desktop client; in headless mode the session is created but no terminal tab appears."),
		actor.WithParams(
			actor.ParamDesc{Name: "HostId", Description: "ID of the saved SSH host to connect to (from sshmanager.host_list)"},
		),
	); err != nil {
		return fmt.Errorf("agent: register open_ssh_session: %w", err)
	}
	if err := ctx.Register("explore_progress", a.handleExploreProgress, actor.Internal()); err != nil {
		return fmt.Errorf("agent: register explore_progress: %w", err)
	}
	if err := ctx.Register("explore_complete", a.handleExploreComplete, actor.Internal()); err != nil {
		return fmt.Errorf("agent: register explore_complete: %w", err)
	}
	if err := ctx.Register("compact", a.handleCompact, actor.Internal(), actor.WithLoop("agent_exec")); err != nil {
		return fmt.Errorf("agent: register compact: %w", err)
	}
	if err := ctx.Register("status_notify", a.handleStatusNotify, actor.Internal(), actor.WithLoop(coordEventLoop)); err != nil {
		return fmt.Errorf("agent: register status_notify: %w", err)
	}
	if err := ctx.Register("title_set", a.handleSetTitle, actor.Internal()); err != nil {
		return fmt.Errorf("agent: register title_set: %w", err)
	}
	if err := ctx.Register("plan_submit", a.handlePlanSubmit, actor.Internal()); err != nil {
		return fmt.Errorf("agent: register plan_submit: %w", err)
	}
	if err := ctx.Register("workflow_plan_submit", a.handleWorkflowPlanSubmit, actor.Internal(), actor.WithLoop("agent_exec")); err != nil {
		return fmt.Errorf("agent: register workflow_plan_submit: %w", err)
	}
	if err := ctx.Register("goal_submit", a.handleGoalSubmit, actor.Internal()); err != nil {
		return fmt.Errorf("agent: register goal_submit: %w", err)
	}
	if err := ctx.Register("goal_card_submit", a.handleGoalCardSubmit, actor.Internal()); err != nil {
		return fmt.Errorf("agent: register goal_card_submit: %w", err)
	}
	if err := ctx.Register("workflow_start", a.handleWorkflowStart, actor.Public(), actor.WithLoop("agent_exec")); err != nil {
		return fmt.Errorf("agent: register workflow_start: %w", err)
	}
	if err := ctx.Register("workflow_stop", a.handleWorkflowStop, actor.Public()); err != nil {
		return fmt.Errorf("agent: register workflow_stop: %w", err)
	}
	if err := ctx.Register("workflow_pause_all", a.handleWorkflowPauseAll, actor.Public()); err != nil {
		return fmt.Errorf("agent: register workflow_pause_all: %w", err)
	}
	if err := ctx.Register("turn_assess", a.handleTurnAssess, actor.Internal()); err != nil {
		return fmt.Errorf("agent: register turn_assess: %w", err)
	}
	if err := ctx.Register("task_create", a.handleTaskCreate, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithToolName("create_task"),
	); err != nil {
		return fmt.Errorf("agent: register task_create: %w", err)
	}
	if err := ctx.Register("task_update", a.handleTaskUpdate, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithToolName("update_task"),
	); err != nil {
		return fmt.Errorf("agent: register task_update: %w", err)
	}
	if err := ctx.Register("task_list", a.handleTaskList, actor.Public()); err != nil {
		return fmt.Errorf("agent: register task_list: %w", err)
	}
	if err := ctx.Register("task_delete", a.handleTaskDelete, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
	); err != nil {
		return fmt.Errorf("agent: register task_delete: %w", err)
	}
	if err := ctx.Register("task_cancel", a.handleTaskCancel, actor.Public(),
		actor.WithEffect(string(domain.EffectNone)),
	); err != nil {
		return fmt.Errorf("agent: register task_cancel: %w", err)
	}
	if err := ctx.Register("memory_save", a.handleMemorySave, actor.Public()); err != nil {
		return fmt.Errorf("agent: register memory_save: %w", err)
	}
	if err := ctx.Register("memory_recall", a.handleMemoryRecall, actor.Public()); err != nil {
		return fmt.Errorf("agent: register memory_recall: %w", err)
	}
	if err := ctx.Register("memory_snapshot", a.handleMemorySnapshot, actor.Public()); err != nil {
		return fmt.Errorf("agent: register memory.snapshot: %w", err)
	}
	if err := ctx.Register("memory_dream", a.handleMemoryDream, actor.Public()); err != nil {
		return fmt.Errorf("agent: register memory_dream: %w", err)
	}
	if err := ctx.Register("memory_weave_apply", a.handleMemoryWeaveApply, actor.Internal()); err != nil {
		return fmt.Errorf("agent: register memory_weave_apply: %w", err)
	}
	if err := ctx.RegisterEventKind("agent_message_received", domain.AgentMessage{}, actor.Public()); err != nil {
		return fmt.Errorf("agent: register agent_message_received event kind: %w", err)
	}
	if err := ctx.Register("message_receive", a.handleReceiveMessage, actor.Internal()); err != nil {
		return fmt.Errorf("agent: register message_receive: %w", err)
	}
	if err := ctx.Register("message_list", a.handleMessageList, actor.Public()); err != nil {
		return fmt.Errorf("agent: register message_list: %w", err)
	}
	if err := ctx.Register("message_clear", a.handleMessageClear, actor.Public()); err != nil {
		return fmt.Errorf("agent: register message_clear: %w", err)
	}
	if err := ctx.Register("agent_configure", a.handleConfigure, actor.AdminOnly()); err != nil {
		return fmt.Errorf("agent: register agent_configure: %w", err)
	}
	if err := ctx.Register("agent_bind_app", a.handleBindApp, actor.Internal()); err != nil {
		return fmt.Errorf("agent: register agent_bind_app: %w", err)
	}
	if err := ctx.Register("compaction_configure", a.handleCompactionConfigure, actor.Public()); err != nil {
		return fmt.Errorf("agent: register compaction_configure: %w", err)
	}
	if err := ctx.Register("storage_configure", a.handleStorageConfigure, actor.Public()); err != nil {
		return fmt.Errorf("agent: register storage_configure: %w", err)
	}
	if err := ctx.Register("complete_message", a.handleCompleteMessage, actor.Public(), actor.WithLoop("agent_exec")); err != nil {
		return fmt.Errorf("agent: register complete_message: %w", err)
	}
	if err := ctx.Register("messages_list", a.handleMessagesList, actor.Public()); err != nil {
		return fmt.Errorf("agent: register messages_list: %w", err)
	}
	if err := ctx.Register("message_read", a.handleMessageRead, actor.Public()); err != nil {
		return fmt.Errorf("agent: register message_read: %w", err)
	}
	if err := ctx.Register("thinking_get_registry", a.handleThinkingGetRegistry, actor.Public()); err != nil {
		return fmt.Errorf("agent: register thinking_get_registry: %w", err)
	}
	if err := ctx.Register("thinking_set_level", a.handleThinkingSetLevel, actor.Public()); err != nil {
		return fmt.Errorf("agent: register thinking_set_level: %w", err)
	}

	// skill_use rides the agent_exec loop lane (same as skill_mount): a
	// runtime:spore skill may run its script for seconds, and that must not
	// block the owner lane.
	if err := ctx.Register("skill_use", a.handleSkillUse, actor.Public(),
		actor.WithLoop("agent_exec"),
		actor.WithDescription("Invoke a mounted skill by skillId and receive its workflow instructions. The skill must be mounted first; each skill can be used at most once per session."),
	); err != nil {
		return fmt.Errorf("agent: register skill_use: %w", err)
	}
	if err := ctx.Register("skill_mount", a.handleSkillInject, actor.Public(), actor.WithLoop("agent_exec")); err != nil {
		return fmt.Errorf("agent: register skill_mount compatibility: %w", err)
	}

	a.actorID = ctx.Self().ID().String()
	if !a.child.Mode && a.actorID == "" {
		ctx.Logger().Error("agent: OnStart missing actorID; persistence disabled")
	}
	a.loadMailbox(ctx)
	a.loadRequestRecords()
	if !a.child.Mode {
		// Initialize/restore the memory graph before any component
		// reconciliation below: syncSkillMounts and friends can call
		// saveMailbox mid-sequence, and those saves must checkpoint the
		// live (restored) graph rather than run with a nil one.
		a.syncMemoryMountState(ctx)
		a.seedBuiltinComponentMounts(ctx)
		changed := a.reconcileBuiltinBundleMounts(ctx)
		changed = a.ensureGoalCardMounted(ctx) || changed
		changed = a.ensureWorkflowCardMounted(ctx) || changed
		changed = a.restoreComponentMountMetadata(ctx) || changed
		cfg := a.fetchAgentKindConfig(ctx)
		if cfg.Kind == "" {
			for _, defaultCfg := range agentkit.BaseKindConfigs() {
				if defaultCfg.Kind == a.agentKind {
					cfg = defaultCfg
					break
				}
			}
		}
		changed = a.syncDefaultCardRefs(cfg) || changed
		if skillChanged, err := a.syncSkillMounts(ctx, cfg); err != nil {
			ctx.Logger().Warn("agent: failed to mount project skills", "error", err)
		} else {
			changed = skillChanged || changed
		}
		changed = a.normalizeSessionTurns() || changed
		changed = a.migrateTurnOrders() || changed
		if changed {
			a.saveMailbox(ctx)
		}
		if a.rebuildSteps() {
			a.saveMailbox(ctx)
		}
		recovered := a.closeOpenStepsForTerminalTurns()
		recovered = a.recoverOrphanTurnSteps() || recovered
		if recovered {
			a.saveMailbox(ctx)
		}
		recovered = a.recoverTurnStatus()
		if a.recoverPendingInteraction(ctx) {
			recovered = true
		}
		if recovered {
			a.saveMailbox(ctx)
		}
	}
	a.snapshotReady = true
	a.takeSnapshot()
	a.refreshPromptCaches(ctx)
	a.refreshWorktreeStatus(ctx)
	a.notifyWorkspaceStatus(ctx)

	// Self-assign a spawn-time goal (from workspace.agent.spawn_assign)
	// after all handlers are registered and initialization is complete.
	// This avoids the cross-actor invoke race: assign_goal arriving before
	// handler registration would be silently dropped by the framework.
	if a.pendingSpawnGoal != nil {
		goal := a.pendingSpawnGoal
		a.pendingSpawnGoal = nil
		a.applySpawnGoal(ctx, goal)
	}

	return nil
}

// recoverTurnStatus restores the agent's derived turn status (a.status) after a
// restart. a.status is not persisted, so without recovery the frontend would
// show idle, hiding paused/failed states the user needs to see. All mutations
// to Session.Turns go through domain.ApplyTurnLifecycleEvent so canonical
// Revision/PauseReason/Error/CompletedAt constraints are enforced. Returns true
// if Session.Turns was mutated and the caller should persist.
func (a *Actor) recoverTurnStatus() bool {
	if a.getActiveTurnRef() == "" {
		for i := len(a.Session.Turns) - 1; i >= 0; i-- {
			turn := a.Session.Turns[i]
			if turn.Role != "assistant" || (turn.State != domain.TurnStatePaused && turn.State != domain.TurnStateRunning && turn.State != domain.TurnStateWaiting) {
				continue
			}
			// Pending-interaction turns are canonicalized by
			// recoverPendingInteraction; don't pre-bind them here.
			if a.pendingInteraction != nil && a.pendingInteraction.TurnID == turn.ID {
				continue
			}
			a.setActiveTurnRef(turn.ID)
			a.status.TurnID = turn.ID
			a.status.State = turn.State
			a.setActiveTurnOrder(turn.TurnOrder)
			break
		}
	}
	ref := a.getActiveTurnRef()
	if ref != "" {
		if order := a.getActiveTurnOrder(); order == 0 {
			a.setActiveTurnOrder(a.turnOrderByID[ref])
		}
		// A turn covered by a pending interaction is canonicalized by
		// recoverPendingInteraction as paused/interaction. Don't overwrite it
		// here as paused/recovery.
		if a.pendingInteraction != nil && !a.pendingInteractionTurnIsTerminal() {
			if a.pendingInteraction.TurnID == ref || (a.pendingInteraction.TurnID == "" && len(a.Session.Turns) > 0 && a.Session.Turns[len(a.Session.Turns)-1].ID == ref) {
				return false
			}
		}
		for i := range a.Session.Turns {
			if a.Session.Turns[i].ID != ref {
				continue
			}
			turn := &a.Session.Turns[i]
			mutated := false
			switch turn.State {
			case domain.TurnStateCompleted, domain.TurnStateCancelled, domain.TurnStateAbandoned:
				// The turn already finished before the restart; a.status.State
				// stays idle. Clear the stale active-turn ref so the UI doesn't
				// treat the agent as paused or leave it with a phantom active turn.
				if ref != "" {
					a.setActiveTurnRef("")
					mutated = true
				}
			case domain.TurnStateFailed:
				a.status.State = domain.TurnStateFailed
				a.status.TurnID = ref
			case domain.TurnStateRunning:
				// Crash recovery: a running turn that is not blocked on a user
				// interaction is unrecoverable as-is. Canonicalize it to
				// paused/recovery so the user can decide whether to resume.
				now := time.Now().UTC().Format(time.RFC3339Nano)
				pausedEvent := domain.TurnLifecycleEvent{
					Kind:        domain.TurnLifecyclePaused,
					TurnID:      ref,
					State:       domain.TurnStatePaused,
					Revision:    turn.Revision + 1,
					TurnOrder:   turn.TurnOrder,
					StartedAt:   turn.StartedAt,
					PauseReason: domain.PauseReasonRecovery,
				}
				updated, _, err := a.applyTurnLifecycleEventToRecord(ref, pausedEvent)
				if err != nil {
					// Unrecoverable — abandon with diagnostic and clear active ref.
					slog.Warn("turn recovery to paused/recovery rejected, abandoning", "turn", ref, "error", err)
					abandonEvent := domain.TurnLifecycleEvent{
						Kind:        domain.TurnLifecycleAbandoned,
						TurnID:      ref,
						State:       domain.TurnStateAbandoned,
						Revision:    turn.Revision + 1,
						TurnOrder:   turn.TurnOrder,
						StartedAt:   turn.StartedAt,
						Error:       fmt.Sprintf("unrecoverable turn state after restart: %v", err),
						CompletedAt: now,
					}
					if _, _, err2 := a.applyTurnLifecycleEventToRecord(ref, abandonEvent); err2 != nil {
						slog.Warn("abandon recovery also rejected, leaving turn as-is", "turn", ref, "error", err2)
					}
					a.setActiveTurnRef("")
					a.status.State = domain.TurnStateAbandoned
					a.status.TurnID = ref
					mutated = true
					break
				}
				_ = updated
				mutated = true
				a.status.State = domain.TurnStatePaused
				a.status.TurnID = ref
				a.status.PauseKind = domain.PauseReasonRecovery
			case domain.TurnStatePaused:
				a.status.State = domain.TurnStatePaused
				a.status.TurnID = ref
				a.status.PauseKind = turn.PauseReason
			case domain.TurnStateWaiting:
				// A waiting turn found after restart: the turn was parked waiting for
				// a workflow updater message. If the agent is still workflow-active,
				// preserve it as paused/recovery so turn_resume can restore waiting;
				// otherwise finalize to completed so non-workflow agents don't stay
				// blocked on a phantom active turn.
				now := time.Now().UTC().Format(time.RFC3339Nano)
				if a.workflowActive() {
					pausedEvent := domain.TurnLifecycleEvent{
						Kind:        domain.TurnLifecyclePaused,
						TurnID:      ref,
						State:       domain.TurnStatePaused,
						Revision:    turn.Revision + 1,
						TurnOrder:   turn.TurnOrder,
						StartedAt:   turn.StartedAt,
						CompletedAt: turn.CompletedAt,
						PauseReason: domain.PauseReasonRecovery,
					}
					updated, _, err := a.applyTurnLifecycleEventToRecord(ref, pausedEvent)
					if err != nil {
						slog.Warn("turn recovery waiting→paused rejected, abandoning", "turn", ref, "error", err)
						abandonEvent := domain.TurnLifecycleEvent{
							Kind:        domain.TurnLifecycleAbandoned,
							TurnID:      ref,
							State:       domain.TurnStateAbandoned,
							Revision:    turn.Revision + 1,
							TurnOrder:   turn.TurnOrder,
							StartedAt:   turn.StartedAt,
							Error:       fmt.Sprintf("unrecoverable waiting turn after restart: %v", err),
							CompletedAt: now,
						}
						if _, _, err2 := a.applyTurnLifecycleEventToRecord(ref, abandonEvent); err2 != nil {
							slog.Warn("abandon recovery also rejected, leaving turn as-is", "turn", ref, "error", err2)
						}
						a.setActiveTurnRef("")
						a.status.State = domain.TurnStateAbandoned
						a.status.TurnID = ref
						mutated = true
						break
					}
					_ = updated
					mutated = true
					a.status.State = domain.TurnStatePaused
					a.status.TurnID = ref
					a.status.PauseKind = domain.PauseReasonRecovery
				} else {
					completedEvent := domain.TurnLifecycleEvent{
						Kind:        domain.TurnLifecycleCompleted,
						TurnID:      ref,
						State:       domain.TurnStateCompleted,
						Revision:    turn.Revision + 1,
						TurnOrder:   turn.TurnOrder,
						StartedAt:   turn.StartedAt,
						CompletedAt: turn.CompletedAt,
					}
					_, _, err := a.applyTurnLifecycleEventToRecord(ref, completedEvent)
					if err != nil {
						slog.Warn("turn recovery waiting→completed rejected, abandoning", "turn", ref, "error", err)
						abandonEvent := domain.TurnLifecycleEvent{
							Kind:        domain.TurnLifecycleAbandoned,
							TurnID:      ref,
							State:       domain.TurnStateAbandoned,
							Revision:    turn.Revision + 1,
							TurnOrder:   turn.TurnOrder,
							StartedAt:   turn.StartedAt,
							Error:       fmt.Sprintf("failed to finalize waiting turn: %v", err),
							CompletedAt: now,
						}
						if _, _, err2 := a.applyTurnLifecycleEventToRecord(ref, abandonEvent); err2 != nil {
							slog.Warn("abandon recovery also rejected, leaving turn as-is", "turn", ref, "error", err2)
						}
					}
					a.setActiveTurnRef("")
					a.status.State = ""
					a.status.TurnID = ""
					mutated = true
				}
			default:
				// Unknown or non-canonical state — the turn cannot be safely
				// recovered. Abandon it and record the diagnostic.
				now := time.Now().UTC().Format(time.RFC3339Nano)
				slog.Warn("unknown turn state after restart, abandoning", "turn", ref, "state", turn.State)
				abandonEvent := domain.TurnLifecycleEvent{
					Kind:        domain.TurnLifecycleAbandoned,
					TurnID:      ref,
					State:       domain.TurnStateAbandoned,
					Revision:    turn.Revision + 1,
					TurnOrder:   turn.TurnOrder,
					StartedAt:   turn.StartedAt,
					Error:       fmt.Sprintf("unknown turn state %q after restart; cannot safely recover", turn.State),
					CompletedAt: now,
				}
				if _, _, err := a.applyTurnLifecycleEventToRecord(ref, abandonEvent); err != nil {
					slog.Warn("abandon recovery rejected, leaving turn as-is", "turn", ref, "error", err)
				}
				a.setActiveTurnRef("")
				a.status.State = domain.TurnStateAbandoned
				a.status.TurnID = ref
				mutated = true
			}
			return mutated
		}
		// ActiveTurnRef references a turn that no longer exists (e.g. discarded
		// by compaction/crash). Clear it so the agent doesn't appear stuck with
		// a phantom active turn.
		if ref != "" {
			a.setActiveTurnRef("")
			return true
		}
		return false
	}
	// No active turn to resume, but the last turn may have ended in a terminal
	// "failed" state. Without restoring it the frontend shows idle after a
	// restart, hiding the error the user needs to see and act on.
	if n := len(a.Session.Turns); n > 0 {
		last := a.Session.Turns[n-1]
		last.State = domain.NormalizeTurnState(last.State)
		if last.State == domain.TurnStateFailed {
			a.status.State = domain.TurnStateFailed
			a.status.TurnID = last.ID
		}
	}
	return false
}

// isTerminalTurnState reports whether a turn state is terminal (no further work
// or resume is possible). Used by the restart-recovery path to detect stale
// pendingInteraction records that survived a crash past turn completion.
func isTerminalTurnState(s string) bool {
	if s == domain.TurnStateLegacyResumed {
		return true
	}
	return domain.IsTerminalTurnState(s)
}

// pendingInteractionTurnIsTerminal reports whether the turn referenced by the
// current pendingInteraction is already in a terminal state. Returns false when
// there is no pending interaction or the referenced turn cannot be found (so the
// caller treats it as a live, recoverable wait).
func (a *Actor) pendingInteractionTurnIsTerminal() bool {
	pi := a.pendingInteraction
	if pi == nil || pi.TurnID == "" {
		return false
	}
	for _, t := range a.Session.Turns {
		if t.ID == pi.TurnID {
			return isTerminalTurnState(t.State)
		}
	}
	return false
}

// recoverPendingInteraction restores the interaction flags from a persisted
// pendingInteraction record after a restart and canonicalizes the associated turn
// as paused/interaction. The plan_approval flag is already restored in loadMailbox;
// this covers ask_user / permission / goal_submit / goal_card_submit /
// workflow_start. It must run after recoverTurnStatus.
//
// The associated turn is set to paused with PauseReason=interaction so the
// interaction UI (driven by the restored flags and pauseReason) is presented to
// the user instead of leaving the turn running. All Session.Turns writes go
// through domain.ApplyTurnLifecycleEvent.
//
// Returns true if Session.Turns or the pendingInteraction record was mutated and
// the caller should persist.
func (a *Actor) recoverPendingInteraction(ctx actor.Context) bool {
	pi := a.pendingInteraction
	if pi == nil {
		return false
	}

	// Determine the turn this interaction belongs to.
	turnID := pi.TurnID
	if turnID == "" {
		turnID = a.getActiveTurnRef()
	}
	if turnID == "" && len(a.Session.Turns) > 0 {
		last := a.Session.Turns[len(a.Session.Turns)-1]
		if !isTerminalTurnState(last.State) {
			turnID = last.ID
		}
	}
	if turnID == "" {
		// No live turn to attach to; the interaction is stale.
		a.clearPendingInteraction(ctx)
		return true
	}

	var turn *domain.Turn
	var turnIdx int
	for i := range a.Session.Turns {
		if a.Session.Turns[i].ID == turnID {
			turn = &a.Session.Turns[i]
			turnIdx = i
			break
		}
	}
	if turn == nil || isTerminalTurnState(turn.State) {
		// The referenced turn is missing or already finished; the interaction was
		// resolved before the restart but the record survived (e.g. crash between
		// resolution and mailbox save). Drop it so the agent doesn't appear stuck.
		a.clearPendingInteraction(ctx)
		return true
	}

	switch pi.Type {
	case "ask_user":
		a.pendingAskUser = true
	case "permission":
		a.pendingApproval = true
	case "goal_submit":
		a.goalSubmitPending = true
		a.goalSubmitRequestID = pi.RequestID
	case "goal_card_submit":
		a.goalCardSubmitPending = true
		a.goalCardSubmitRequestID = pi.RequestID
		a.goalCardProposal = goalCardProposalFromTask(pi.Task)
	case "workflow_start":
		// Restore the in-memory flag and the persisted goal-derived state from
		// RawSession.PendingWorkflowStart. The interaction event itself is not
		// re-emitted; the mailbox replay shows the existing step.
		if a.RawSession.PendingWorkflowStart == nil {
			// The pendingInteraction record has no matching persisted workflow
			// state, so treat it as stale and drop it.
			a.clearPendingInteraction(ctx)
			return true
		}
		a.workflowStartPending = true
		a.workflowStartRequestID = pi.RequestID
	case "plan_approval":
		// planApprovalPending restored in loadMailbox; nothing to do here.
	}

	// A crash can leave the pending interaction durable while its open step was
	// not written. Rebuild that step so session.summary renders the interaction
	// instead of exposing the turn as a generic recovery pause.
	mutated := false
	if pi.StepID != "" {
		found := false
		for _, step := range a.steps {
			if step.ID == pi.StepID {
				found = true
				break
			}
		}
		if !found {
			a.applyStepEvent(domain.StepEvent{
				Kind:            "step.interaction_requested",
				StepID:          pi.StepID,
				TurnID:          turnID,
				InteractionType: pi.Type,
				RequestID:       pi.RequestID,
				Task:            pi.Task,
				Seq:             a.allocSeq(),
			})
			mutated = true
		}
	}

	// Canonicalize the turn as paused/interaction so the UI presents the
	// interaction instead of showing a running turn stuck in the engine.
	if turn.State != domain.TurnStatePaused || turn.PauseReason != domain.PauseReasonInteraction {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		event := domain.TurnLifecycleEvent{
			Kind:        domain.TurnLifecyclePaused,
			TurnID:      turnID,
			State:       domain.TurnStatePaused,
			Revision:    turn.Revision + 1,
			TurnOrder:   turn.TurnOrder,
			StartedAt:   turn.StartedAt,
			PauseReason: domain.PauseReasonInteraction,
		}
		updated, _, err := a.applyTurnLifecycleEventToRecord(turnID, event)
		if err != nil {
			// Unrecoverable — abandon the turn and clear the interaction.
			slog.Warn("pending interaction recovery to paused/interaction rejected, abandoning", "turn", turnID, "error", err)
			abandonEvent := domain.TurnLifecycleEvent{
				Kind:        domain.TurnLifecycleAbandoned,
				TurnID:      turnID,
				State:       domain.TurnStateAbandoned,
				Revision:    turn.Revision + 1,
				TurnOrder:   turn.TurnOrder,
				StartedAt:   turn.StartedAt,
				Error:       fmt.Sprintf("pending interaction recovery failed: %v", err),
				CompletedAt: now,
			}
			if _, _, err2 := a.applyTurnLifecycleEventToRecord(turnID, abandonEvent); err2 != nil {
				slog.Warn("abandon recovery also rejected, leaving turn as-is", "turn", turnID, "error", err2)
			}
			a.clearPendingInteraction(ctx)
			a.setActiveTurnRef("")
			a.status.State = domain.TurnStateAbandoned
			a.status.TurnID = turnID
			return true
		}
		_ = updated
		turn = &a.Session.Turns[turnIdx]
		mutated = true
	}

	a.setActiveTurnRef(turnID)
	a.setActiveTurnOrder(turn.TurnOrder)
	a.status.State = domain.TurnStatePaused
	a.status.TurnID = turnID
	a.status.PauseKind = domain.PauseReasonInteraction
	return mutated
}

// effectivePauseKind returns the pause kind surfaced by status reporting: an
// explicit PauseKind wins; a paused agent with pending task records falls back
// to "task" (the existing TurnPauseKind projection semantics). Empty when not
// paused or when no kind is derivable.
func (a *Actor) effectivePauseKind() string {
	if a.status.State != "paused" {
		return ""
	}
	if a.status.PauseKind != "" {
		return a.status.PauseKind
	}
	if len(a.RawSession.Tasks) > 0 {
		return "task"
	}
	return ""
}

// Active turn ref/order helpers.
// Production code must use these to mutate ActiveTurnRef/activeTurnOrder; the
// turnEngine flush goroutine may read them concurrently via takeSnapshot.

func (a *Actor) setActiveTurnRef(ref string) {
	a.turnRefMu.Lock()
	a.ActiveTurnRef = ref
	a.turnRefMu.Unlock()
}

func (a *Actor) getActiveTurnRef() string {
	a.turnRefMu.RLock()
	defer a.turnRefMu.RUnlock()
	return a.ActiveTurnRef
}

func (a *Actor) setActiveTurnOrder(order int64) {
	a.turnRefMu.Lock()
	a.activeTurnOrder = order
	a.turnRefMu.Unlock()
}

func (a *Actor) getActiveTurnOrder() int64 {
	a.turnRefMu.RLock()
	defer a.turnRefMu.RUnlock()
	return a.activeTurnOrder
}

// activeTurnSnapshot returns ActiveTurnRef and activeTurnOrder captured under a
// single turnRefMu acquisition so takeSnapshot observes a consistent pair even
// when the owner loop mutates them concurrently.
func (a *Actor) activeTurnSnapshot() (string, int64) {
	a.turnRefMu.RLock()
	defer a.turnRefMu.RUnlock()
	return a.ActiveTurnRef, a.activeTurnOrder
}

// takeSnapshot captures the current session/turn state into an atomic snapshot
// for lock-free concurrent reads from the pure session.summary handler.
// Must be called on the cell goroutine (where all stateful mutations happen).

func (a *Actor) takeSnapshot() {
	// Publish turn-level observable state via gospore components so the
	// topology provider can enrich nodes without synchronous invokes.
	// Idle is represented explicitly as "idle" so the topology graph doesn't
	// fall back to a generic "running" default for agents with no active turn.
	if a.status.State != "" {
		a.TurnState = a.status.State
	} else {
		a.TurnState = "idle"
	}
	a.TurnPauseKind = a.effectivePauseKind()

	er := make([]domain.ExploreResult, len(a.RawSession.ExploreResults))
	copy(er, a.RawSession.ExploreResults)

	tasks := make([]gen.TurnTask, len(a.RawSession.Tasks))
	copy(tasks, a.RawSession.Tasks)

	steps := make([]domain.Step, len(a.steps))
	copy(steps, a.steps)

	segs := make([]domain.SummarySegment, len(a.RawSession.SummarySegments))
	copy(segs, a.RawSession.SummarySegments)

	// Copy open step events from the active turn engine for reconnect replay.
	var openStepEvents map[string][]domain.StepEvent
	if eng := a.activeTurnEngine(); eng != nil {
		eng.stepEventMu.Lock()
		openStepEvents = make(map[string][]domain.StepEvent, len(eng.openStepEvents))
		for sid, events := range eng.openStepEvents {
			openStepEvents[sid] = append([]domain.StepEvent(nil), events...)
		}
		eng.stepEventMu.Unlock()
	}

	// Snapshot mailbox for the pure agent.message.list handler.
	mailbox := make([]domain.AgentMessage, len(a.mailbox))
	copy(mailbox, a.mailbox)

	// Build the flat chat-history list (steps + active turn delta) for the
	// pure agent.messages.list handler. Idx is assigned from the flat position
	// for pagination. Step entries come from msgCache: only slots whose step
	// changed since the last publish are re-converted; the published slice is
	// a private copy so later cache-slot replacement never penetrates it.
	if len(a.msgCache) != len(a.steps) {
		// A steps write bypassed the choke points (or a test seeded a.steps
		// directly) — repair the invariant with a full rebuild.
		a.rebuildMsgCache()
	}
	for i := range a.steps {
		if a.msgDirty[i] {
			a.msgCache[i] = stepToChatMessage(a.steps[i])
			a.msgCache[i].Idx = int32(i)
			a.msgDirty[i] = false
		}
	}
	delta := a.fetchActiveTurnDelta()
	messages := make([]domain.ChatMessage, 0, len(a.msgCache)+len(delta))
	messages = append(messages, a.msgCache...)
	for _, msg := range delta {
		msg.Idx = int32(len(messages))
		messages = append(messages, msg)
	}

	activeTurnRef, activeTurnOrder := a.activeTurnSnapshot()
	a.snapshot.Store(&sessionSnapshotData{
		ready:           a.snapshotReady,
		turns:           a.Session.Turns,
		activeHead:      a.Session.ActiveHead,
		activeTurnRef:   activeTurnRef,
		turnID:          a.status.TurnID,
		turnStatus:      a.buildTurnStatusReadOnly(activeTurnRef, activeTurnOrder),
		statusState:     a.status.State,
		statusPauseKind: a.effectivePauseKind(),
		exploreResults:  er,
		tasks:           tasks,
		steps:           steps,
		summarySegments: segs,
		goal:            a.RawSession.Goal,
		nextIdx:         a.RawSession.NextIdx,
		nextSeq:         a.RawSession.NextSeq,
		mailbox:         mailbox,
		messages:        messages,
		openStepEvents:  openStepEvents,
	})
	a.snapshotFlushedAt.Store(time.Now().UnixNano())
}

// snapshotStreamMinInterval rate-limits takeSnapshot rebuilds on the streaming
// path. The dispatch receive loop's delta-flush ticker (60ms) retries
// naturally, so the snapshot converges within ~2 ticks while per-chunk cost
// drops from O(session) to a single atomic load. Boundary calls (turn end,
// state mutations, emitStepImmediate) bypass this via takeSnapshot directly.
const snapshotStreamMinInterval = 100 * time.Millisecond

// takeSnapshotDebounced is the streaming-path onAfterFlush: it skips the
// O(session)-sized snapshot rebuild when the previous one is fresher than
// snapshotStreamMinInterval. Safe to call from any goroutine (atomic load).
func (a *Actor) takeSnapshotDebounced() {
	if flushed := a.snapshotFlushedAt.Load(); flushed != 0 &&
		time.Since(time.Unix(0, flushed)) < snapshotStreamMinInterval {
		return
	}
	a.takeSnapshot()
}

// refreshPromptCaches rebuilds the prompt-artifact / compiled-prompt snapshots
// on the cell goroutine and stores them atomically so the pure
// agent.prompt_artifact / agent.compiled_prompt handlers can serve them off
// the owner queue. Called from OnStart and after each turn compile.
func (a *Actor) refreshPromptCaches(ctx actor.Context) {
	artifact := a.buildPromptArtifactStateful(ctx)
	a.cachedPromptArtifact.Store(&artifact)
	compiled := a.buildCompiledPromptStateful(ctx)
	a.cachedCompiledPrompt.Store(&compiled)
}

// loadThinkingRegistry returns the current thinking-level registry as a map.
// The returned map is shared (not a copy); callers must not mutate it.
func (a *Actor) loadThinkingRegistry() map[thinkingUnitKey]gen.ThinkingLevel {
	if p := a.thinkingRegistry.Load(); p != nil {
		return *p
	}
	return nil
}

// storeThinkingLevel copies-on-write stores a single thinking-level override
// into the atomic registry so the pure get_registry handler observes it.
func (a *Actor) storeThinkingLevel(key thinkingUnitKey, level gen.ThinkingLevel) {
	cur := a.loadThinkingRegistry()
	next := make(map[thinkingUnitKey]gen.ThinkingLevel, len(cur)+1)
	for k, v := range cur {
		next[k] = v
	}
	next[key] = level
	a.thinkingRegistry.Store(&next)
}

func (a *Actor) agentMeta() string {
	if a.actorID != "" {
		return "agent|" + a.actorID + "|" + a.DisplayName
	}
	return "agent||"
}

// normalizePermissionMode maps any input to a valid permission mode; unknown
// or empty values fall back to "permission" (confirm per call).
func normalizePermissionMode(mode string) string {
	switch mode {
	case "yolo", "allow-all", "auto", "autopilot":
		return mode
	default:
		return "permission"
	}
}

// NewActor returns a Props factory for a regular top-level agent.
func NewActor(workspaceID, kind, displayName string, primary, fast, execution, review, summary domain.ModelSlot, spawnGoal *domain.AgentInternalAssignGoalReq, permissionMode string, parentAgentID string, extraBundleIDs []string) func() actor.Actor {
	return func() actor.Actor {
		return &Actor{
			workspaceID:      workspaceID,
			agentKind:        kind,
			DisplayName:      displayName,
			primary:          primary,
			fast:             fast,
			execution:        execution,
			review:           review,
			summary:          summary,
			onStartDone:      make(chan struct{}),
			pendingSpawnGoal: spawnGoal,
			permissionMode:   normalizePermissionMode(permissionMode),
			parentAgentID:    parentAgentID,
			extraBundleIDs:   extraBundleIDs,
		}
	}
}

// NewChildActor returns a Props factory for a short-lived forked sub-agent.
// The child receives [Task: <description>]\n<prompt> as its initial user
// message, plus recent parent conversation context appended as
// <parent_context>...</parent_context>, plus resolved hot context (git
// status, project roots, profile fragments), and uses a read-oriented tool
// surface (read-only kinds such as explorer/reviewer never receive
// spawn-time extra bundles; general children keep the full surface).
// It auto-starts an exploration turn on boot. Progress and results are
// forwarded back to the parent agent via agent.explore_progress and
// agent.explore_complete.
//
// primary is the resolved primary-purpose slot (parent's resolveChildSlot
// priority). fast/execution/review/summary copy the parent's non-primary
// runtime slots so the child inherits the same model configuration; empty
// ([auto]) slots arrive as zero values and the child keeps the default.
// extraBundleIDs are spawn-time bundles (e.g. plugin-dev for dev-app
// project agents) seeded on top of the kind defaults, subject to the
// read-only kind gate in builtinCardsToSeed.

func NewChildActor(
	workspaceID string,
	agentKind string,
	parentRef ref.Ref,
	parentTurnID string,
	parentStepID string,
	parentToolUseID string,
	task, prompt string,
	primary, fast, execution, review, summary domain.ModelSlot,
	maxIterations int32,
	hotContext []domain.ContentBlock,
	extraBundleIDs []string,
) func() actor.Actor {
	return func() actor.Actor {
		displayName := pickExplorerName()
		if agentKind != "explorer" {
			displayName = pickGeneralName()
		}
		msgs := []domain.ChatMessage{{
			Role: domain.ChatRoleUser,
			Content: []domain.ContentBlock{{
				Type: domain.ContentBlockText,
				Text: fmt.Sprintf("[Task: %s]\n%s", task, prompt),
			}},
		}}
		steps := make([]domain.Step, len(msgs))
		for i, msg := range msgs {
			steps[i] = domain.Step{
				ID:        fmt.Sprintf("child-init-%d", i),
				Role:      msg.Role,
				Type:      "text",
				Content:   msg.Content,
				Closed:    true,
				Timestamp: time.Now().UTC().Format(time.RFC3339),
				Seq:       int64(i + 1),
				Meta:      "user",
			}
		}
		return &Actor{
			workspaceID:    workspaceID,
			agentKind:      agentKind,
			DisplayName:    displayName,
			onStartDone:    make(chan struct{}),
			extraBundleIDs: extraBundleIDs,
			child: childState{
				Mode:          true,
				AgentRef:      parentRef,
				TurnRef:       parentTurnID,
				StepRef:       parentStepID,
				ToolUseID:     parentToolUseID,
				MaxIterations: maxIterations,
				HotContext:    hotContext,
			},
			status: turnStatus{
				Unit: slotFirstUnit(primary),
			},
			primary:   primary,
			fast:      fast,
			execution: execution,
			review:    review,
			summary:   summary,
			steps:     steps,
			RawSession: domain.RawSession{
				NextIdx: int32(len(msgs)),
				// Seed steps already occupy Seq 1..len(steps). Start the
				// allocator above them so the first turn-driven step does
				// not collide with the seed user step on Seq=1 (which the
				// frontend now prefers over timestamp for ordering).
				NextSeq: int64(len(steps) + 1),
			},
		}
	}
}

// ── Child mode ──

// onStartChild 是子 agent（child.Mode=true）的启动路径。
// 与常规 agent 相比，callable 面最小化：只保留 turn 执行、状态查询和
// explore_complete / explore_progress 回调。注册完成后，自动调用
// handleChildStartExplore 启动第一次探索 turn。

func (a *Actor) OnStop(ctx actor.Context) error {
	a.stopStatusNotifications()
	a.saveMailbox(ctx)
	return nil
}

// OnDestroy persists the mailbox before the actor is removed from the tree.

func (a *Actor) OnDestroy(ctx actor.Context) error {
	a.saveMailbox(ctx)
	return nil
}

// callablesMap builds a callableID → CallableInterface lookup from the topology
// snapshot. If multiple actors expose the same callable name, the first one
// encountered in the snapshot wins.

func (a *Actor) handlePermissionReview(_ actor.PureContext, _ domain.PermissionReviewReq) (domain.PermissionReviewResp, error) {
	// Default policy: allow all. Concrete agent kinds may override.
	return domain.PermissionReviewResp{Allowed: true, Reason: "default allow"}, nil
}

type setPermissionModeReq struct {
	Mode string `json:"Mode"`
}

func (a *Actor) handleSetPermissionMode(ctx actor.Context, req setPermissionModeReq) error {
	mode := normalizePermissionMode(req.Mode)
	a.permissionMode = mode
	a.permissionModeOverride = true
	// Persist immediately so the override survives actor re-spawns (e.g. a
	// workflow owner agent woken after a process restart re-loads its state
	// instead of falling back to the default "permission" mode).
	a.saveMailbox(ctx)
	ctx.Logger().Info("agent: permission mode set", "mode", mode)
	// Push the updated mode to the workspace so the frontend snapshot reflects
	// the live value (not a stale global default).
	a.notifyWorkspaceStatus(ctx)
	return nil
}

func (a *Actor) handleStatus(ctx actor.PureContext) (gen.AgentStatusResp, error) {
	childLastActivityNs := int32(0)
	if a.child.Mode {
		childLastActivityNs = int32(a.child.LastActivityNs.Load())
	}
	lastActivity := a.lastActivityAt()
	lastTurn := a.lastTurnCompletedAt()
	goalSummary := a.goalSummary()
	snap := a.snapshot.Load()
	if snap == nil {
		return gen.AgentStatusResp{
			// a.status.State may already be set by OnStart recovery (e.g. "paused"
			// or "failed") before takeSnapshot runs. Returning it here lets the
			// workspace's refreshAgentStatuses poll see the correct state even if
			// it wins the race against snapshotReady, instead of reporting empty
			// (idle) and hiding paused/error after a restart.
			State:                    a.status.State,
			PauseKind:                a.effectivePauseKind(),
			Primary:                  slotPtr(a.primary),
			Fast:                     slotPtr(a.fast),
			Execution:                slotPtr(a.execution),
			Review:                   slotPtr(a.review),
			Summary:                  slotPtr(a.summary),
			CurrentUnit:              agentUnitPtr(a.status.Unit),
			DisplayName:              a.DisplayName,
			Title:                    a.title,
			LastActivity:             lastActivity,
			LastTurnCompletedAt:      lastTurn,
			Goal:                     goalSummary,
			ActiveWorkflowMapCardID:  a.activeWorkflowMapID(),
			ActiveWorkflowWorktreeID: a.activeWorkflowWorktreeID(),
			PendingWorkflowStart:     a.pendingWorkflowStart(),
			ChildLastActivitySec:     childLastActivityNs,
		}, nil
	}
	return gen.AgentStatusResp{
		ActiveTurnRef:            snap.activeTurnRef,
		State:                    snap.statusState,
		PauseKind:                snap.statusPauseKind,
		Primary:                  slotPtr(a.primary),
		Fast:                     slotPtr(a.fast),
		Execution:                slotPtr(a.execution),
		Review:                   slotPtr(a.review),
		Summary:                  slotPtr(a.summary),
		CurrentUnit:              agentUnitPtr(a.status.Unit),
		DisplayName:              a.DisplayName,
		Title:                    a.title,
		LastActivity:             lastActivity,
		LastTurnCompletedAt:      lastTurn,
		Goal:                     goalSummary,
		ActiveWorkflowMapCardID:  a.activeWorkflowMapID(),
		ActiveWorkflowWorktreeID: a.activeWorkflowWorktreeID(),
		PendingWorkflowStart:     a.pendingWorkflowStart(),
		ChildLastActivitySec:     childLastActivityNs,
	}, nil
}

func (a *Actor) pendingWorkflowStart() *gen.PendingWorkflowStart {
	return a.RawSession.PendingWorkflowStart
}

func (a *Actor) activeWorkflowMapID() string {
	if a.RawSession.ActiveWorkflow == nil {
		return ""
	}
	return a.RawSession.ActiveWorkflow.MapCardID
}

func (a *Actor) activeWorkflowWorktreeID() string {
	if a.RawSession.ActiveWorkflow == nil {
		return ""
	}
	return a.RawSession.ActiveWorkflow.WorktreeID
}

func (a *Actor) goalSummary() *gen.GoalSummary {
	g := a.RawSession.Goal
	if g == nil {
		return nil
	}
	return &gen.GoalSummary{
		Condition:       g.Condition,
		Status:          g.Status,
		Confirmed:       g.Confirmed,
		BoundTaskCardID: g.BoundTaskCardID,
		TurnCount:       g.TurnCount,
		MaxTurns:        g.MaxTurns,
		Outputs:         g.Outputs,
	}
}

func (a *Actor) handleStatusNotify(ctx actor.Context, _ gen.AgentStatusNotifyReq) error {
	a.notifyWorkspaceStatus(ctx)
	return nil
}

// markStarted closes onStartDone exactly once. It is deferred at the top of
// OnStart so the pure session.summary handler's cold-start wait unblocks
// regardless of how OnStart exits. Safe against double-close (e.g. an actor
// instance whose OnStart runs more than once without being recreated): the
// select's receive case fires immediately on an already-closed channel, so
// close only runs while it is still open. Called solely from the cell
// goroutine, so no external synchronization.
func (a *Actor) markStarted() {
	if a.onStartDone == nil {
		return
	}
	select {
	case <-a.onStartDone:
	default:
		close(a.onStartDone)
	}
}

// MaxTitleLen is the hard truncation ceiling for an agent's title, counted in
// runes so multi-byte characters (CJK, emoji) are never split. The intent
// prompt targets ~20 characters, but models routinely overshoot; this ceiling
// stops runaway titles (whole paragraphs) from being persisted or projected.
// Applied uniformly to the LLM-inference path (agent.title.set) and the
// explicit user-edit path (agent.configure).
const MaxTitleLen = 64

// truncateTitle caps s to MaxTitleLen runes without splitting multi-byte
// characters. It is a no-op when s is already within the ceiling.
func truncateTitle(s string) string {
	if utf8.RuneCountInString(s) <= MaxTitleLen {
		return s
	}
	return string([]rune(s)[:MaxTitleLen])
}

// applyGoalTitle mirrors a confirmed goal intent into the agent title so
// list/avatar surfaces show the user-confirmed goal text. The confirmed
// interpretation is ground truth and overwrites any inferred title.
func (a *Actor) applyGoalTitle(ctx actor.Context, interpretedGoal, condition string) {
	text := interpretedGoal
	if text == "" {
		text = condition
	}
	if text == "" {
		return
	}
	title := truncateTitle(text)
	if a.title == title {
		return
	}
	a.title = title
	a.Title = title
	a.notifyWorkspaceStatus(ctx)
}

func (a *Actor) handleSetTitle(ctx actor.Context, req setTitleReq) error {
	if req.Title == "" {
		return nil
	}
	if a.title != "" {
		ctx.Logger().Debug("agent: inferred title dropped — title already set",
			"existing", a.title, "inferred", req.Title)
		return nil
	}
	title := truncateTitle(req.Title)
	a.title = title
	a.Title = title
	// Persist the inferred title so it survives process restarts.
	a.saveMailbox(ctx)
	// Push the updated title to workspace/frontend.
	a.notifyWorkspaceStatus(ctx)
	return nil
}

func (a *Actor) currentTaskSummary() string {
	for _, t := range a.RawSession.Tasks {
		if t.Status == "in_progress" && t.Subject != "" {
			return t.Subject
		}
	}
	return ""
}

// hasParentAgent reports whether this agent was spawned by another agent
// (workspace.agent_spawn_assign). Only agents with a parent have an external
// reviewer for their bound task card; root agents are reviewed by the user
// directly and must not pause on ready_for_review.
func (a *Actor) hasParentAgent() bool {
	return a.parentAgentID != ""
}

// boundTaskCardID returns the workflow task card this agent's goal is bound
// to (empty when unbound), surfaced to the workspace agent list so the
// workflow topology can map task cards to their working agent.
func (a *Actor) boundTaskCardID() string {
	if a.RawSession.Goal == nil {
		return ""
	}
	return a.RawSession.Goal.BoundTaskCardID
}

// lastTurnCompletedAt returns the most recent terminal turn's completion time.
// Only CompletedAt is considered so the timestamp reflects an actual finished
// turn, not an in-flight turn's start time.
func (a *Actor) lastTurnCompletedAt() string {
	for i := len(a.Session.Turns) - 1; i >= 0; i-- {
		if t := a.Session.Turns[i]; t.CompletedAt != "" {
			return t.CompletedAt
		}
	}
	return ""
}

// lastActivityAt returns the most recent meaningful activity timestamp from
// the session's turn history. For a completed turn that is CompletedAt; for
// an in-flight turn (no CompletedAt yet) it falls back to StartedAt.
func (a *Actor) lastActivityAt() string {
	for i := len(a.Session.Turns) - 1; i >= 0; i-- {
		t := a.Session.Turns[i]
		if t.CompletedAt != "" {
			return t.CompletedAt
		}
		if t.StartedAt != "" {
			return t.StartedAt
		}
	}
	return ""
}

func (a *Actor) activeThinkLevelString(ctx actor.PureContext) string {
	if a.activeThinkLevel != "" {
		return a.activeThinkLevel
	}
	unit := a.status.Unit
	if unit.Model == "" {
		unit = a.slotFirstUnitResolved(ctx, a.primary)
	}
	if unit.Model == "" {
		return ""
	}
	if lvl, ok := a.loadThinkingRegistry()[thinkingUnitKey{Model: unit.Model, Provider: unit.Provider}]; ok {
		return lvl.Mode
	}
	return ""
}

// enterWorktreeMode binds the agent to an isolated worktree via
// project.worktree_enter. Both worktree-mode entry points route through here —
// the /worktree slash command (handleChatSubmit) and agent.component.mount
// (handleComponentMountInternal) — so the mode card is never mounted without an
// active binding; project.worktree_exit remains the only unbind/delete path.
// Idempotent: an already-bound agent re-enters its existing worktree unchanged.
func (a *Actor) enterWorktreeMode(ctx actor.Context) error {
	parent := ctx.Parent()
	planner := ctx.Planner()
	if parent == nil || planner == nil {
		return fmt.Errorf("agent: worktree mode requires a project parent")
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	if _, err := planner.Call(callCtx, parent, "project.worktree_enter", gen.ProjectWorktreeEnterReq{}).Await(); err != nil {
		return fmt.Errorf("agent: enter worktree: %w", err)
	}
	// Cache the binding (worktreePath et al) and sync the mode card before
	// the turn runs, so shell/file tooling resolves the worktree root.
	a.refreshWorktreeStatus(ctx)
	return nil
}

// exitWorktreeMode discards the agent's bound worktree via
// project.worktree_exit (mode=discard, force). Called from the confirmed
// unmount path of builtin:mode:worktree: the user has agreed to lose the
// worktree including uncommitted changes.
func (a *Actor) exitWorktreeMode(ctx actor.Context) error {
	parent := ctx.Parent()
	planner := ctx.Planner()
	if parent == nil || planner == nil {
		return fmt.Errorf("agent: worktree mode requires a project parent")
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	if _, err := planner.Call(callCtx, parent, "project.worktree_exit", gen.ProjectWorktreeExitReq{Mode: "discard", Force: true}).Await(); err != nil {
		return fmt.Errorf("agent: exit worktree: %w", err)
	}
	a.worktreeID = ""
	a.worktreeName = ""
	a.worktreeStatus = ""
	a.worktreePath = ""
	return nil
}

// requireWorktreeUnmountConfirm gates unmounting builtin:mode:worktree. The
// unmount discards the worktree — a destructive act the UI must confirm
// first. Returns nil when the unmount may proceed (confirmed or the agent
// has no worktree binding anyway).
func (a *Actor) requireWorktreeUnmountConfirm(req domain.AgentComponentUnmountReq) error {
	if req.CardID != "builtin:mode:worktree" {
		return nil
	}
	if a.worktreeStatus == "" || a.worktreeStatus == "none" {
		return nil
	}
	if !req.Confirm {
		return fmt.Errorf("agent.component.unmount: unmounting worktree mode discards worktree %q including uncommitted changes; requires Confirm after user acknowledgement", a.worktreeName)
	}
	return nil
}

func (a *Actor) refreshWorktreeStatus(ctx actor.Context) {
	parent := ctx.Parent()
	planner := ctx.Planner()
	if parent == nil || planner == nil {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	result, err := planner.Call(callCtx, parent, "project.worktree_agent_bindings", gen.ProjectWorktreeAgentBindingsReq{}).Await()
	if err != nil || result == nil {
		return
	}
	var resp gen.ProjectWorktreeAgentBindingsResp
	switch v := result.(type) {
	case gen.ProjectWorktreeAgentBindingsResp:
		resp = v
	case *gen.ProjectWorktreeAgentBindingsResp:
		if v == nil {
			return
		}
		resp = *v
	case []byte:
		if json.Unmarshal(v, &resp) != nil {
			return
		}
	default:
		encoded, marshalErr := json.Marshal(v)
		if marshalErr != nil || json.Unmarshal(encoded, &resp) != nil {
			return
		}
	}
	a.worktreeID = ""
	a.worktreeName = ""
	a.worktreeStatus = ""
	a.worktreePath = ""
	for _, binding := range resp.Bindings {
		if binding.AgentActorID == a.actorID {
			a.worktreeID = binding.WorktreeID
			a.worktreeName = binding.Name
			a.worktreeStatus = binding.Status
			a.worktreePath = binding.WorktreePath
			break
		}
	}
	a.syncWorktreeModeCard(ctx)
	a.invalidateComponentSnapshot(ctx)
}

// syncWorktreeModeCard keeps the builtin:mode:worktree component mount in sync
// with the agent's current worktree binding. It mounts the card when the agent
// enters a worktree and unmounts it when the agent leaves, so the composer
// badge and worktree-specific tools follow the same lifecycle as goal mode.
// Fork children (child.Mode) are skipped: they may inherit the parent's
// worktree binding purely for tool path routing, but worktree mode — the mode
// card, its badge, and its exit/discard lifecycle — belongs to the parent that
// owns the worktree.
func (a *Actor) syncWorktreeModeCard(ctx actor.Context) {
	if a.child.Mode {
		return
	}
	const cardID = "builtin:mode:worktree"
	active := a.worktreeStatus != "" && a.worktreeStatus != "none"
	mounted := a.cardRefEnabled(cardID)
	switch {
	case active && !mounted:
		if a.ensureComponentCardMounted(ctx, cardID, "builtin", true) {
			a.saveMailbox(ctx)
		}
	case !active && mounted:
		a.handleComponentUnmount(ctx, domain.AgentComponentUnmountReq{CardID: cardID})
	}
}

// setPendingInteraction records the one blocking interaction currently awaiting
// a user answer and persists it, so the blocked state survives an agent
// restart. Called at each interaction emit point after the
// step.interaction_requested event. Any prior pending interaction is
// overwritten (at most one interaction blocks a turn at a time).
func (a *Actor) setPendingInteraction(ctx actor.Context, turnID, stepID, requestID, typ string, task map[string]any) {
	a.pendingInteraction = &pendingInteractionState{
		TurnID:    turnID,
		StepID:    stepID,
		RequestID: requestID,
		Type:      typ,
		Task:      task,
	}
	a.saveMailbox(ctx)
}

func (a *Actor) notifyWorkspaceStatus(ctx actor.Context) {
	_, ok := ctx.LookupService("workspace")
	if !ok {
		return
	}
	req := gen.WorkspaceAgentStatusUpdateReq{
		AgentActorID:             a.actorID,
		State:                    a.status.State,
		ActiveTurnRef:            a.getActiveTurnRef(),
		ApprovalPending:          a.pendingApproval,
		PlanApprovalPending:      a.planApprovalPending,
		AskUserPending:           a.pendingAskUser,
		GoalSubmitPending:        a.goalSubmitPending,
		CurrentTaskSummary:       a.currentTaskSummary(),
		BoundTaskCardID:          a.boundTaskCardID(),
		ActiveWorkflowMapCardID:  a.activeWorkflowMapID(),
		ActiveWorkflowWorktreeID: a.activeWorkflowWorktreeID(),
		LastActivity:             a.lastActivityAt(),
		LastTurnCompletedAt:      a.lastTurnCompletedAt(),
		ThinkLevel:               a.activeThinkLevelString(ctx),
		WorktreeID:               a.worktreeID,
		WorktreeStatus:           a.worktreeStatus,
		WorktreeName:             a.worktreeName,
		MemoryMounted:            a.Graph != nil,
		Title:                    a.title,
		PermissionMode:           a.permissionMode,
	}
	if a.status.State == "failed" {
		// Surface the most recent failed turn's error if available.
		for i := len(a.Session.Turns) - 1; i >= 0; i-- {
			if t := a.Session.Turns[i]; t.State == "failed" && t.Error != "" {
				req.Error = t.Error
				break
			}
		}
	}
	// Store the latest snapshot and CAS-arm a single sender goroutine. The
	// sender sends updates synchronously one at a time, so ordering is
	// preserved; intermediate states collapse to the latest, which always wins.
	a.statusNotifyLatest.Store(&req)
	if a.statusNotifyInflight.CompareAndSwap(false, true) {
		done := make(chan struct{})
		a.statusNotifyDone.Store(&done)
		go a.flushStatusNotifications(ctx, done)
	}
}

// flushStatusNotifications is the single-flight sender: it repeatedly takes
// the newest pending snapshot and invokes workspace.agent_status_update with
// it. Runs until no update remains; a notify racing the exit path re-arms the
// gate so no update is lost.
func (a *Actor) flushStatusNotifications(ctx actor.Context, done chan struct{}) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("agent: flushStatusNotifications panic: %v\n%s\n", r, debug.Stack())
		}
		close(done)
	}()
	wsRef, ok := ctx.LookupService("workspace")
	lifecycle := ctx.Lifecycle()
	for {
		req := a.statusNotifyLatest.Swap(nil)
		if req == nil {
			// Release the gate, then re-check: a notify that stored between
			// Swap and the gate release must not be stranded.
			a.statusNotifyInflight.Store(false)
			if a.statusNotifyLatest.Load() == nil {
				return
			}
			if !a.statusNotifyInflight.CompareAndSwap(false, true) {
				return // another sender took over
			}
			continue
		}
		if !ok || wsRef == nil {
			continue
		}
		// Attach the live primary route chain to the snapshot being sent only
		// when a chain mutation (record/demote) armed it. Consuming the flag
		// here (send time, not build time) guarantees the LATEST snapshot
		// carries Primary even when a state change notify overwrites the armed
		// snapshot before it is sent. Ordinary ticks never arm the flag, so
		// they never carry a Primary and cannot clobber a concurrent user
		// reconfigure.
		if a.routeReportPending.CompareAndSwap(true, false) {
			a.slotMu.RLock()
			req.Primary = slotPtr(a.primary)
			a.slotMu.RUnlock()
		}
		callCtx, cancel := context.WithTimeout(lifecycle, 5*time.Second)
		_ = wsRef.Invoke(callCtx, "workspace.agent_status_update", *req)
		cancel()
	}
}

// stopStatusNotifications waits for the in-flight sender (if any) to finish,
// ensuring no in-flight update outlives the actor. Notifies arriving during
// teardown re-arm the sender but their invokes fail fast on the canceled
// lifecycle context.
func (a *Actor) stopStatusNotifications() {
	for a.statusNotifyInflight.Load() {
		p := a.statusNotifyDone.Load()
		if p == nil {
			return
		}
		<-*p
	}
}

// activeTurnEngine returns the current inline turn engine, or nil if no turn
// is running. Access is atomic so it is safe from the default loop while
// agent.run sets/clears it on agent.exec.
func (a *Actor) activeTurnEngine() *turnEngine {
	return a.turnEngine.Load()
}

// turnEngineStore sets or clears the inline turn engine pointer.
func (a *Actor) turnEngineStore(e *turnEngine) {
	a.turnEngine.Store(e)
}

func (a *Actor) handleInspectPages(actor.PureContext) (domain.InspectPagesResp, error) {
	return domain.InspectPagesResp{
		Items: []domain.InspectPage{
			{ID: "prompt-context", Label: "Prompt Context", Icon: "FileText"},
			{ID: "compiled-prompt", Label: "Compiled Prompt", Icon: "Send"},
			{ID: "turn-history", Label: "Turn History", Icon: "MessageSquare"},
			{ID: "compaction-snapshot", Label: "Compaction", Icon: "Layers"},
			{ID: "context-budget", Label: "Context Budget", Icon: "BarChart3"},
			{ID: "component-snapshot", Label: "Components", Icon: "Database"},
		},
	}, nil
}
