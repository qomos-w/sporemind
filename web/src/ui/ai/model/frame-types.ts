// ── Frame Protocol ──
// Canonical envelope/frame protocol for AI Shell rendering.

import type { ContextSegment as __ContextSegment, CompactedRange as __CompactedRange, CompactionRoundData as __CompactionRoundData, SessionGoal as __SessionGoal } from '../../../gen-types/aigen'
import type { TurnRequestStat as __TurnRequestStat } from '../../../gen-types/aistats'

export type SessionGoal = __SessionGoal

export type ContextSegment = __ContextSegment
export type CompactedRange = __CompactedRange
export type CompactionRound = __CompactionRoundData

export type TurnRequestStat = __TurnRequestStat

export type SlotStatus = 'pending' | 'running' | 'completed' | 'error' | 'cancelled'

export interface FileChangeEntry {
  filename: string
  filepath?: string
  icon?: 'tsx' | 'ts' | 'css' | 'json' | 'md' | 'other'
  additions?: number
  deletions?: number
  diffContent?: string
}

export interface SourceEntryData {
  title: string
  url?: string
  snippet?: string
}

export interface AttachmentEntry {
  name: string
  mimeType: string
  url?: string
  sizeBytes?: number
}

export interface TurnUsage {
  inputTokens?: number
  outputTokens?: number
  totalTokens?: number
}

export interface PendingSubmitEntry {
  id: string
  text: string
  timestamp: string
}

/** Optimistic response stored locally when the user answers an interaction
 *  (ask_user / permission / plan approval / goal submit / goal card submit)
 *  before the backend confirms. */
export interface LocalInteractionResponse {
  kind: 'ask_answered' | 'permission_answered' | 'plan_approval_answered' | 'goal_submit_answered' | 'goal_card_submit_answered'
  answers?: Record<number, string>
  allowed?: boolean
  decision?: 'approve' | 'reject' | 'edit' | 'confirm_goal' | 'start_workflow'
  editedPlan?: string
  feedback?: string
  selectedPolicy?: PlanFrame['policy']
}

export interface TurnMetadata {
  turnId?: string
  actionCount?: number
  elapsedSeconds?: number
  usage?: TurnUsage
  /** Per-frame last-known token snapshot — used to compute deltas for cumulative usage. */
  _frameUsageMap?: Record<string, TurnUsage>
  activeFrameId?: string
  activeFrameUsage?: TurnUsage
  model?: string
  contextBudget?: ContextBudget
  /** Derived turn state from step statuses and lifecycle events. Vocabulary:
   *  running, paused, waiting, completed, failed, cancelled, abandoned. 'resumed'
   *  is not a frontend state; turn.resumed events map to 'running'. */
  turnState?: 'running' | 'paused' | 'waiting' | 'failed' | 'completed' | 'cancelled' | 'abandoned'
  /** Error message for failed turns, populated from turn.failed events. */
  error?: string
  /** Live dispatch-retry indicator (1-indexed retry number), set while the
   *  turn engine is backing off between failed LLM dispatch attempts. */
  retryCount?: number
  /** Maximum dispatch retries allowed for the current turn. */
  retryMax?: number
  /** Truncated dispatch failure text shown with the retry indicator
   *  (e.g. provider 429 quota message with reset time). */
  retryError?: string
  /** Current turn loop state (Dispatch / Execute / WaitChild / ...). Set by turn.loop_state_changed events. */
  loopState?: string
  /** Canonical monotonic revision for this turn record. Used to discard stale
   *  lifecycle events and to let summary records overwrite live event cache. */
  revision?: number
  startedAt?: string
  completedAt?: string
  pendingSubmits?: PendingSubmitEntry[]
  /** Per-delivery telemetry records for the turn, used to render cost/cache stats in the budget bar. */
  requestStats?: TurnRequestStat[]
  worktreeId?: string
  worktreeName?: string
  worktreeStatus?: string
}

export interface ContextBudget {
  estimatedTokens: number
  contextWindowSize: number
  tokenBudget: number
}

export type FrameAnimation = 'fade' | 'slide' | 'scale' | 'none'

export type TimelineConnectorMode = 'to-next-icon' | 'none'

export interface BaseFrame {
  id: string
  status: SlotStatus
  animation?: FrameAnimation
}

export type ContextKind = 'cold' | 'hot' | 'summary' | 'message'

export interface CompactionFrame extends BaseFrame {
  type: 'compaction'
  trigger: 'auto' | 'user'
  beforeTokens: number
  afterTokens: number
  contextWindowSize: number
  /** Ordered segment layout before compaction */
  beforeLayout: ContextSegment[]
  /** Ordered segment layout after all rounds */
  afterLayout: ContextSegment[]
  rounds: CompactionRound[]
  model?: string
  error?: string
}

export type StructuredFrame =
  | TextFrame
  | PlanFrame
  | ReasoningFrame
  | ToolFrame
  | SourcesFrame
  | AttachmentsFrame
  | ImageFrame
  | ErrorFrame
  | AskUserQuestionFrame
  | PermissionRequestFrame
  | CompactionFrame
  | UserInjectFrame
  | SkillUseFrame
  | MemorySaveFrame
  | GoalReviewFrame
  | GoalSubmitFrame
  | GoalCardSubmitFrame

export interface TextFrame extends BaseFrame {
  type: 'text'
  content: string
  /** Model that produced this assistant text frame (per-step attribution). */
  model?: string
}

export interface PlanFrame extends BaseFrame {
  type: 'plan'
  content: string
  requestId?: string
  tasks?: TaskEntry[]
  policy?: { mode: string; allowedPrompts?: { tool: string; prompt: string }[] }
  editable?: boolean
  goalActive?: boolean
  approvalStatus?: 'pending' | 'approved' | 'rejected' | 'cancelled'
}

export interface ReasoningFrame extends BaseFrame {
  type: 'reasoning'
  content: string
  durationSeconds?: number
  inputTokens?: number
  outputTokens?: number
  /** Model that produced this reasoning frame (per-step attribution). */
  model?: string
}

export interface ToolFrame extends BaseFrame {
  type: 'tool'
  toolName: string
  /** Raw JSON string from the LLM tool_use block. Parse in the View via parseJsonObject — keep reducer derivation-free. */
  input: string
  output?: string
  snapshotRef?: SnapshotRef
  fileChanges?: FileChangeEntry[]
  durationSeconds?: number
  inputTokens?: number
  outputTokens?: number
  exitCode?: number
  callableId?: string
  targetService?: string
  /** Forced-call approval: the LLM emitted a tool call the turn engine
   *  routed to a forced-dispatch branch, and the user must confirm the
   *  call's args (or supply alternatives) before the call goes through.
   *  Drives the schema input modal in [[schema-overlay-input-modal]]: a
   *  click on the step opens the shared modal so the user can edit the
   *  args before submission. Absent for normal tool calls (which still
   *  show their input/output in the right-panel detail view). */
  forcedApproval?: {
    /** Callable ID the user will dispatch (defaults to `callableId`). */
    targetCallable: string
    /** Optional schema ID (uint64) to resolve via the schema registry. */
    schemaId?: number
    /** The args the LLM emitted — used as the draft the user can edit. */
    draftArgs?: Record<string, unknown>
    /** Free-form note rendered above the form (e.g. "Tool choice forced this call"). */
    note?: string
  }
  /** Explore progress — populated by turn.explore_progress events. */
  progress?: {
    phase?: string
    searchCount?: number
    readCount?: number
    inputTokens?: number
    outputTokens?: number
    activeWork?: { Type: string; Target: string }[]
    summaryText?: string
  }
  /** Live stdout/stderr for shell tools — populated by turn.execution_output_delta
   *  events. Absent on history replay (frame.output is the source of truth there). */
  runningOutput?: {
    stdout: string
    stderr: string
    frozen?: boolean
  }
}

export interface SnapshotRef {
  id: string
  path: string
  offset?: number
  limit?: number
  size: number
  /** Real read span from the backend response (1-based). Present for project.read snapshots. */
  startLine?: number
  numLines?: number
  totalLines?: number
}

export interface SourcesFrame extends BaseFrame {
  type: 'sources'
  entries: SourceEntryData[]
}

export interface AttachmentsFrame extends BaseFrame {
  type: 'attachments'
  files: AttachmentEntry[]
}

export interface ImageFrame extends BaseFrame {
  type: 'image'
  url: string
  alt?: string
}

export interface ErrorFrame extends BaseFrame {
  type: 'error'
  message: string
  code?: string
  /** Name of the failed tool call, extracted from the step's tool_use block.
   *  Present only for tool_call-step errors; llm_call / turn-level errors
   *  leave it undefined so the renderer falls back to a generic title. */
  toolName?: string
}

export interface AskUserOption {
  label: string
  description?: string
  recommended?: boolean
}

export interface AskUserQuestion {
  header: string
  question: string
  options: AskUserOption[]
  multiSelect: boolean
}

export interface AskUserQuestionFrame extends BaseFrame {
  type: 'ask_user'
  questions: AskUserQuestion[]
  answers?: Record<number, string>
  requestId?: string
}

export interface PermissionRequestFrame extends BaseFrame {
  type: 'permission_request'
  toolCalls: { id: string; callableId: string; input?: string; appId?: string; appName?: string; permissions?: string[]; permissionNote?: string }[]
  reason?: string
  allowed?: boolean
  requestId?: string
}

export interface UserInjectFrame extends BaseFrame {
  type: 'user_inject'
  items: Array<{ id: string; text: string; idx?: number }>
}

export interface SkillUseFrame extends BaseFrame {
  type: 'skill_use'
  skillName: string
  isError?: boolean
}

export interface MemorySaveFrame extends BaseFrame {
  type: 'memory_save'
  /** Saved memory content (from the tool_use input). */
  content: string
  /** Memory layer, defaults to 'session'. */
  layer?: string
  /** Node ID assigned by the backend (from memory_result output). Empty until confirmed. */
  nodeId?: string
  isError?: boolean
}

export interface GoalReviewFrame extends BaseFrame {
  type: 'goal_review'
  condition: string
  achieved?: boolean
  reason?: string
  turnCount?: number
  maxTurns?: number
  aborted?: boolean
  planCardCount?: number
  /** Live reviewer tool activity, populated by step.execution_progress events. */
  progress?: {
    phase?: string
    searchCount?: number
    readCount?: number
    summaryText?: string
    activeWork?: { Type: string; Target: string }[]
  }
}

export interface GoalSubmitFrame extends BaseFrame {
  type: 'goal_submit'
  condition: string
  interpretedGoal: string
  requestId: string
  approvalStatus: 'pending' | 'approved' | 'rejected' | 'cancelled'
}

/** Existing task-card binding awaiting user confirmation (goal_card_submit interaction). */
export interface GoalCardSubmitFrame extends BaseFrame {
  type: 'goal_card_submit'
  cardId: string
  interpretedGoal?: string
  requestId: string
  approvalStatus: 'pending' | 'approved' | 'rejected' | 'cancelled'
}

export interface UIFrame extends BaseFrame {
  type: 'ui'
  html: string
}

export type Frame = StructuredFrame | UIFrame

export type FrameType = Frame['type']

export interface TaskEntry {
  id: string
  subject: string
  status: 'pending' | 'in_progress' | 'completed'
  activeForm?: string
}

export interface TurnEnvelope {
  id: string
  /** Stable client-side key that survives `replaceUserMessageId` — prevents
   *  React key change / remount when a pending user envelope gets its real id. */
  clientKey?: string
  role: 'user' | 'assistant'
  userContent?: string
  userAttachments?: AttachmentEntry[]
  userImages?: { url: string; alt?: string }[]
  frames: Frame[]
  goal?: SessionGoal
  tasks?: TaskEntry[]
  fileChanges?: FileChangeEntry[]
  timestamp: string
  metadata?: TurnMetadata
  completed?: boolean
  /** Session-level monotonic message index assigned by turn. Used for
   *  cross-client continuity checks and gap detection. */
  idx?: number
  /** Session-level monotonic step sequence number. */
  seq?: number
  /** Session-level turn sequence number. Preferred for envelope ordering. */
  turnSeq?: number
  /** True when this user envelope originated from an injected message
   *  (IsInject=true from backend). Renders as an expandable TimelineStep
   *  instead of a plain user bubble. */
  isInject?: boolean
  /** Categorization for system-originated user envelopes (Meta=goal or
   *  Meta=workflow). When set, the envelope renders as a compact
   *  system-continuation bubble instead of a user bubble. */
  systemOrigin?: 'goal' | 'workflow'
  /** Sender identity for user envelopes whose step Meta is
   *  "agent|<id>|<name>" (peer agent message) or "user|<id>|<name>"
   *  (human inject). When set the bubble renders with the sender's avatar
   *  on the left; for agent senders the avatar navigates to that agent's
   *  conversation. */
  peerSender?: { kind: 'agent' | 'user'; id: string; name: string }
  /** Internal entry drafts — source of truth for step/delta events.
   *  Not serialized; frames are the public projection. */
  _entries?: EntryDraft[]
}

export interface SlotEntry {
  id: string
  frame: Frame
  version: number
}

// ── Entry Draft (internal reducer state) ──
// The reducer maintains entry drafts as the source of truth for
// streaming step/delta events. Frames are projected from entries.

export interface BlockDraft {
  id: string
  kind: string
  text?: string
  dataJson?: string
  state?: string
  error?: string
  name?: string
  callableId?: string
  targetService?: string
  fileChanges?: FileChangeEntry[]
  /** Set by read_base64 path so blockToFrame can project an image frame. */
  imageUrl?: string
  imageAlt?: string
}

export interface EntryDraft {
  id: string
  kind: string
  title?: string
  state?: string
  error?: string
  callableId?: string
  targetService?: string
  blocks: BlockDraft[]
}
