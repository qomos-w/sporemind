// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { AICallableUnitView, CompactionEvent, ModelUnit, Step, TurnContextBudgetPayload, UsageData } from './aigen.part1';
import { ActiveSchedulerEntry, ActiveWorkflow, AgentModeState, CompactionLockState, PendingWorkflowStart, TurnAssessment } from './aigen.part4';
import { ModelSlot, StoragePolicy } from './aigen.part3';
import { AttachmentEntry, ImageEntry } from './agent.chat';
import { TurnRequestStat } from './aistats';

export interface AIAggregatorStatusResp {
  Id: string;
  Name: string;
  Version: number;
  UnitCount: number;
  Disabled?: boolean | undefined;
  Units: AICallableUnitView[];
}

export interface AgentRef {
  Id: string;
  ActorId: string;
  ProjectId: string;
  ProjectName?: string | undefined;
  DisplayName: string;
  AgentKind: string;
  LoadState?: string | undefined;
  Mode?: AgentModeState | undefined;
  Status?: string | undefined;
  LastActivity?: string | undefined;
  ActiveTurnRef?: string | undefined;
  Primary?: ModelSlot | undefined;
  Fast?: ModelSlot | undefined;
  Execution?: ModelSlot | undefined;
  Review?: ModelSlot | undefined;
  Summary?: ModelSlot | undefined;
  Title?: string | undefined;
  CompactionPolicy?: CompactionPolicy | undefined;
  StoragePolicy?: StoragePolicy | undefined;
  Degraded?: boolean | undefined;
  DegradedReason?: string | undefined;
  ParentAgentId?: string | undefined;
  LifecycleScope?: string | undefined;
  DeletionStatus?: string | undefined;
  Children?: AgentChildRef[] | undefined;
  BoundAppId?: string | undefined;
  BoundAppSlot?: string | undefined;
}

export interface AgentChildRef {
  Id: string;
  ActorId: string;
  ParentAgentId?: string | undefined;
  DisplayName?: string | undefined;
  AgentKind?: string | undefined;
  LifecycleScope?: string | undefined;
  Status?: string | undefined;
  ConversationTarget?: string | undefined;
  ToolUseId?: string | undefined;
  LastActivity?: string | undefined;
}

export interface AgentRefListResp {
  Items: AgentRef[];
}

export interface AgentConfigureReq {
  Primary?: ModelSlot | undefined;
  Fast?: ModelSlot | undefined;
  Execution?: ModelSlot | undefined;
  Review?: ModelSlot | undefined;
  Summary?: ModelSlot | undefined;
  Title?: string | undefined;
}

export interface CompactionPolicy {
  Enabled: boolean;
  Strategy: string;
  TriggerKind: string;
  BudgetMode?: string | undefined;
  TokenBudget?: number | undefined;
  RecentWindow?: number | undefined;
  SummaryUnit?: ModelUnit | undefined;
  MaxSummaryTokens?: number | undefined;
}

export interface SummarySegment {
  SourceStartIndex: number;
  SourceEndIndex: number;
  Level: number;
  Text: string;
  InputTokens: number;
  OutputTokens: number;
  CreatedAt: string;
}

export interface AgentConfig {
  RoleId: string;
  ModelId: string;
  PromptIDs: string[];
  SkillIDs: string[];
  ScriptId: string;
  CompactionPolicy?: CompactionPolicy | undefined;
}

export interface ThinkingLevel {
  Mode: string;
  Effort?: string | undefined;
  Budget?: number | undefined;
}

export interface ThinkingRegistryEntry {
  Unit: ModelUnit;
  Level: ThinkingLevel;
}

export interface SetThinkingLevelReq {
  Unit: ModelUnit;
  Level: ThinkingLevel;
}

export interface GetThinkingRegistryResp {
  Entries: ThinkingRegistryEntry[];
}

export interface PlanAllowedPrompt {
  Tool: string;
  Prompt: string;
}

export interface TurnPlanPolicy {
  Mode: string;
  AllowedPrompts?: PlanAllowedPrompt[] | undefined;
}

export interface TurnPlanApprovalRequestedPayload {
  TurnId: string;
  RequestId: string;
  Plan: string;
  Tasks?: TurnTask[] | undefined;
  Policy?: TurnPlanPolicy | undefined;
  Editable?: boolean | undefined;
}

export interface TurnEvent {
  Kind: string;
  TurnId: string;
  Usage?: UsageData | undefined;
  ContextBudget?: TurnContextBudgetPayload | undefined;
  Payload?: Record<string, unknown> | undefined;
}

export interface AggregatorChunk {
  Kind: string;
  Text?: string | undefined;
  Usage?: UsageData | undefined;
  ToolUseId?: string | undefined;
  ToolName?: string | undefined;
  InputDelta?: string | undefined;
  Input?: string | undefined;
  StopReason?: string | undefined;
  ResolvedUnit?: ModelUnit | undefined;
}

export interface StructuredTraceEntry {
  ToolUseId: string;
  CallableId: string;
  Service: string;
  EffectKind: string;
  Input: string;
  Output: string;
  IsError: boolean;
}

export interface StructuredTraceBlock {
  Entries: StructuredTraceEntry[];
}

export interface TurnTask {
  Id: string;
  Subject: string;
  Status: string;
  ActiveForm?: string | undefined;
}

export interface PendingSubmit {
  Id: string;
  Text: string;
  Timestamp: string;
  Attachments?: AttachmentEntry[] | undefined;
  Images?: ImageEntry[] | undefined;
  Meta?: string | undefined;
}

export interface TurnFileChange {
  Path: string;
  Additions: number;
  Deletions: number;
  DiffContent: string;
}

export interface ExploreResult {
  TurnId: string;
  Summary: string;
  SearchCount: number;
  ReadCount: number;
  InputTokens: number;
  OutputTokens: number;
  Timestamp: string;
  StepId?: string | undefined;
}

export interface Turn {
  Id: string;
  Role: string;
  UserInput?: string | undefined;
  State?: string | undefined;
  Assessment?: TurnAssessment | undefined;
  Usage?: UsageData | undefined;
  ContextBudget?: TurnContextBudgetPayload | undefined;
  Tasks?: TurnTask[] | undefined;
  Error?: string | undefined;
  Cancelled?: boolean | undefined;
  Timestamp?: string | undefined;
  FileChanges?: TurnFileChange[] | undefined;
  ExploreResult?: ExploreResult | undefined;
  Unit?: ModelUnit | undefined;
  StartedAt?: string | undefined;
  CompletedAt?: string | undefined;
  Seq?: number | undefined;
  TurnOrder?: number | undefined;
  Revision?: number | undefined;
  PauseReason?: string | undefined;
  ResumeDescriptor?: string | undefined;
  UserAttachments?: AttachmentEntry[] | undefined;
  UserImages?: ImageEntry[] | undefined;
  RequestStats?: TurnRequestStat[] | undefined;
}

export interface RawSessionArtifact {
  Kind: string;
  Visibility: string;
  Retention: string;
  Payload: string;
}

export interface SessionGoal {
  Condition: string;
  MaxTurns: number;
  TurnCount: number;
  CardID?: string | undefined;
  InterpretedGoal?: string | undefined;
  Confirmed?: boolean | undefined;
  PlanCardIDs?: string[] | undefined;
  Status?: string | undefined;
  ReviewID?: string | undefined;
  ReviewTurnID?: string | undefined;
  BoundTaskCardId?: string | undefined;
  Outputs?: Record<string, unknown> | undefined;
}

export interface RawSession {
  SummarySegments: SummarySegment[];
  Artifacts: RawSessionArtifact[];
  CompactionEvents: CompactionEvent[];
  CompactionLock?: CompactionLockState | undefined;
  Tasks?: TurnTask[] | undefined;
  TaskHistory?: Record<string, TurnTask> | undefined;
  ExploreResults?: ExploreResult[] | undefined;
  Steps?: Step[] | undefined;
  NextIdx: number;
  NextSeq: number;
  NextTurnOrder: number;
  RetentionPolicy: string;
  Goal?: SessionGoal | undefined;
  ActiveWorkflow?: ActiveWorkflow | undefined;
  ActiveScheduler?: ActiveSchedulerEntry[] | undefined;
  PendingWorkflowStart?: PendingWorkflowStart | undefined;
}

export interface Session {
  Turns?: Turn[] | undefined;
  ActiveHead?: number | undefined;
}

export interface TurnPromptArtifactReq {
  SinceMsgId?: string | undefined;
}

export interface AgentTurnCompleteReq {
  Turn: Turn;
  NextIdx: number;
  SummarySegments?: SummarySegment[] | undefined;
  CompactionEvents?: CompactionEvent[] | undefined;
  Steps?: Step[] | undefined;
}

export interface AgentSessionUndoReq {

}

export interface AgentSessionForkReq {
  AtTurnId?: string | undefined;
}

export interface AgentSessionForkResp {
  Session: Session;
  SummarySegments?: SummarySegment[] | undefined;
  ExploreResults?: ExploreResult[] | undefined;
  Steps?: Step[] | undefined;
  Goal?: SessionGoal | undefined;
  NextIdx?: number | undefined;
  NextSeq?: number | undefined;
  NextTurnOrder?: number | undefined;
}
