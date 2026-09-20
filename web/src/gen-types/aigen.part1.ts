// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { DispatchActivity, PermissionPolicy } from './aigen.part3';
import { CompactionPolicy, SummarySegment, Turn, TurnEvent, TurnFileChange, TurnPlanApprovalRequestedPayload } from './aigen.part2';
import { AttachmentEntry, ImageEntry } from './agent.chat';

export interface ModelUnit {
  model: string;
  provider: string;
  thinkLevel?: string | undefined;
}

export interface UsageData {
  InputTokens?: number | undefined;
  OutputTokens?: number | undefined;
  TotalTokens?: number | undefined;
  CacheCreationInputTokens?: number | undefined;
  CacheReadInputTokens?: number | undefined;
  EstimatedPromptTokens?: number | undefined;
  MaxContextLength?: number | undefined;
  ReasoningTokens?: number | undefined;
  CostInput?: number | undefined;
  CostOutput?: number | undefined;
  CostCacheRead?: number | undefined;
  CostCacheWrite?: number | undefined;
  CostTotal?: number | undefined;
}

export interface ContextSegment {
  kind: string;
  tokens: number;
  stepIndex: number;
}

export interface CompactedRange {
  segmentIndex: number;
  tokens: number;
}

export interface CompactionRoundRecord {
  Round: number;
  BeforeLayout: ContextSegment[];
  AfterLayout: ContextSegment[];
  CompactedRanges: CompactedRange[];
  SourceStartIndex: number;
  SourceEndIndex: number;
  Level: number;
  CompactedMessageCount: number;
}

export interface CompactionRoundData {
  compactedRanges: CompactedRange[];
  afterLayout: ContextSegment[];
  sourceStartIndex: number;
  sourceEndIndex: number;
  level: number;
  round: number;
  compactedMessageCount: number;
}

export interface CompactionFrameData {
  type: string;
  id: string;
  status: string;
  trigger: string;
  beforeTokens: number;
  afterTokens: number;
  contextWindowSize: number;
  beforeLayout: ContextSegment[];
  afterLayout: ContextSegment[];
  rounds: CompactionRoundData[];
  unit?: ModelUnit | undefined;
  error?: string | undefined;
}

export interface CompactionEvent {
  TurnId: string;
  Trigger: string;
  BeforeTokens: number;
  AfterTokens: number;
  Unit: ModelUnit;
  Level: number;
  Rounds: CompactionRoundRecord[];
  CreatedAt: string;
  Seq?: number | undefined;
  ContextWindowSize?: number | undefined;
}

export interface ContentBlock {
  Type: string;
  Id?: string | undefined;
  Text?: string | undefined;
  ToolUseId?: string | undefined;
  ToolName?: string | undefined;
  Input?: string | undefined;
  IsError?: boolean | undefined;
  CacheControl?: string | undefined;
  ImageUrl?: string | undefined;
  MimeType?: string | undefined;
  Recognized?: boolean | undefined;
  RecognitionText?: string | undefined;
}

export interface ChatMessage {
  Id: string;
  Role: string;
  Content: ContentBlock[];
  ReasoningContent?: string | undefined;
  Idx?: number | undefined;
  Timestamp?: string | undefined;
  Usage?: UsageData | undefined;
  StartedAt?: string | undefined;
  CompletedAt?: string | undefined;
}

export interface ToolSpec {
  Name: string;
  Description?: string | undefined;
  InputSchema: string;
  Type?: string | undefined;
  EffectKind?: string | undefined;
  CallableId?: string | undefined;
  ServiceName?: string | undefined;
  Stream?: boolean | undefined;
  NativeConfig?: Record<string, unknown> | undefined;
}

export interface CompiledInstructions {
  Base?: string[] | undefined;
  Resolved?: string[] | undefined;
}

export interface CompiledCapabilities {
  PrimitiveTools?: ToolSpec[] | undefined;
}

export interface MiddlewareEntry {
  Hook: string;
  Middleware: string;
  Params?: Record<string, unknown> | undefined;
}

export interface CompiledContext {
  Instructions?: CompiledInstructions | undefined;
  Environment?: string | undefined;
  HotContext?: ContentBlock[] | undefined;
  Messages?: ChatMessage[] | undefined;
  Capabilities?: CompiledCapabilities | undefined;
  Middleware?: MiddlewareEntry[] | undefined;
  PermissionPolicy?: PermissionPolicy | undefined;
  NextIdx: number;
  CompactionPolicy?: CompactionPolicy | undefined;
  SummaryUnit?: ModelUnit | undefined;
  ContextWindowSize?: number | undefined;
  TokenBudget?: number | undefined;
  SummarySegments?: SummarySegment[] | undefined;
  ProjectRoot?: string | undefined;
  MaxIterations?: number | undefined;
}

export interface TurnInput {
  Text: string;
  Unit?: ModelUnit | undefined;
  Title?: string | undefined;
  ThinkingBudget?: number | undefined;
  ReasoningEffort?: string | undefined;
  Attachments?: AttachmentEntry[] | undefined;
  Images?: ImageEntry[] | undefined;
  Meta?: string | undefined;
}

export interface TurnStartReq {
  TurnId: string;
  Input: TurnInput;
  CompiledContext: CompiledContext;
}

export interface TurnAnswerReq {
  AnswersJson: string;
  RequestId?: string | undefined;
}

export interface TurnAction {
  Id: string;
  Kind: string;
  Title?: string | undefined;
  Text?: string | undefined;
  State?: string | undefined;
  Error?: string | undefined;
  StartedAt?: string | undefined;
  CompletedAt?: string | undefined;
  ParentStepId?: string | undefined;
  Input?: string | undefined;
  Output?: string | undefined;
  Usage?: UsageData | undefined;
  Meta?: string | undefined;
  CallableId?: string | undefined;
  TargetService?: string | undefined;
  ToolUseId?: string | undefined;
  Seq?: number | undefined;
}

export interface Step {
  Id: string;
  Role: string;
  Type: string;
  Content: ContentBlock[];
  Closed: boolean;
  Error?: string | undefined;
  Timestamp?: string | undefined;
  StartedAt?: string | undefined;
  CompletedAt?: string | undefined;
  TurnId?: string | undefined;
  ReasoningContent?: string | undefined;
  Usage?: UsageData | undefined;
  ContentStatus?: string | undefined;
  ExecutionStatus?: string | undefined;
  InteractionStatus?: string | undefined;
  Progress?: string | undefined;
  Seq?: number | undefined;
  RequestId?: string | undefined;
  Meta?: string | undefined;
  Discarded?: boolean | undefined;
  Model?: string | undefined;
  Provider?: string | undefined;
}

export interface StepEvent {
  Kind: string;
  StepId: string;
  TurnId: string;
  StepType?: string | undefined;
  Role?: string | undefined;
  Time?: string | undefined;
  Block?: ContentBlock | undefined;
  BlockIndex?: number | undefined;
  Delta?: string | undefined;
  Error?: string | undefined;
  Usage?: UsageData | undefined;
  InteractionType?: string | undefined;
  TaskId?: string | undefined;
  Task?: Record<string, unknown> | undefined;
  FileChanges?: TurnFileChange[] | undefined;
  Progress?: string | undefined;
  RequestId?: string | undefined;
  EventSeq?: number | undefined;
  Seq?: number | undefined;
  OriginMessageId?: string | undefined;
  Meta?: string | undefined;
  Discarded?: boolean | undefined;
  Model?: string | undefined;
  Provider?: string | undefined;
}

export interface TurnBlock {
  Id: string;
  Kind: string;
  Text?: string | undefined;
  DataJson?: string | undefined;
  State?: string | undefined;
  Error?: string | undefined;
  Name?: string | undefined;
  Path?: string | undefined;
  MimeType?: string | undefined;
  ToolUseId?: string | undefined;
  CallableId?: string | undefined;
  TargetService?: string | undefined;
  Usage?: UsageData | undefined;
}

export interface TurnEntry {
  Id: string;
  Kind: string;
  Title?: string | undefined;
  State?: string | undefined;
  StartedAt?: string | undefined;
  CompletedAt?: string | undefined;
  ParentEntryId?: string | undefined;
  CallableId?: string | undefined;
  TargetService?: string | undefined;
  Blocks?: TurnBlock[] | undefined;
  Meta?: string | undefined;
}

export interface TurnContextBudgetPayload {
  EstimatedTokens: number;
  ContextWindowSize: number;
  TokenBudget: number;
}

export interface TurnStatus {
  Turn: Turn;
  Unit?: ModelUnit | undefined;
  StartedAt?: string | undefined;
  UpdatedAt?: string | undefined;
  EventSeq?: number | undefined;
  PlanMode?: boolean | undefined;
  PlanTask?: string | undefined;
  PlanApproval?: TurnPlanApprovalRequestedPayload | undefined;
}

export interface TurnRecordEventsReq {
  Events: TurnEvent[];
}

export interface SystemContentBlock {
  Type: string;
  Text: string;
  CacheControl?: string | undefined;
}

export interface SendSessionMessageReq {
  SessionId: string;
  WorkspaceId?: string | undefined;
  ProjectId?: string | undefined;
  AgentId?: string | undefined;
  TurnId?: string | undefined;
  Unit?: ModelUnit | undefined;
  Title?: string | undefined;
  System?: string | undefined;
  SystemBlocks?: SystemContentBlock[] | undefined;
  Messages?: ChatMessage[] | undefined;
  Tools?: ToolSpec[] | undefined;
  ThinkingBudget?: number | undefined;
  ReasoningEffort?: string | undefined;
  Temperature?: number | undefined;
  TopP?: number | undefined;
  FrequencyPenalty?: number | undefined;
  PresencePenalty?: number | undefined;
  Seed?: number | undefined;
  ToolChoice?: string | undefined;
  SlotKind?: string | undefined;
  UnitPinned?: boolean | undefined;
}

export interface SummarizeResp {
  Text: string;
}

export interface ProbeTokensResp {
  Usage: UsageData;
}

export interface AggregatorDescriptor {
  Id: string;
  Name: string;
  ActorId: string;
  Strategy?: string | undefined;
  Disabled?: boolean | undefined;
}

export interface AggregatorDescriptorListResp {
  Items: AggregatorDescriptor[];
}

export interface AICallableUnitView {
  Id: string;
  Model: string;
  Endpoint: string;
  ProviderName: string;
  Protocol: string;
  aggregatorID?: string | undefined;
  UserAgent?: string | undefined;
  MaxContextLength?: number | undefined;
  Modality?: string | undefined;
  CooldownUntil?: number | undefined;
  DisableUntil?: number | undefined;
  Disabled?: boolean | undefined;
  CooldownReason?: string | undefined;
  ConsecutiveFailures?: number | undefined;
  OnDemand?: boolean | undefined;
  HealthState?: string | undefined;
  HealthReason?: string | undefined;
  LastFailureAt?: number | undefined;
  RecoveryMode?: string | undefined;
  DispatchActivity?: DispatchActivity | undefined;
}
