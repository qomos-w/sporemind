// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { ModelSlot } from './aigen.part3';
import { ModelUnit } from './aigen.part1';
import { PendingWorkflowStart } from './aigen.part4';
import { TurnFileChange } from './aigen.part2';
import { CallableInterface } from './observation';

export interface AgentStatusResp {
  ActiveTurnRef?: string | undefined;
  State?: string | undefined;
  PauseKind?: string | undefined;
  Primary?: ModelSlot | undefined;
  Fast?: ModelSlot | undefined;
  Execution?: ModelSlot | undefined;
  Review?: ModelSlot | undefined;
  Summary?: ModelSlot | undefined;
  CurrentUnit?: ModelUnit | undefined;
  DisplayName?: string | undefined;
  Title?: string | undefined;
  LastActivity?: string | undefined;
  LastTurnCompletedAt?: string | undefined;
  Goal?: GoalSummary | undefined;
  ActiveWorkflowMapCardId?: string | undefined;
  ActiveWorkflowWorktreeID?: string | undefined;
  PendingWorkflowStart?: PendingWorkflowStart | undefined;
  ChildLastActivitySec?: number | undefined;
}

export interface CompleteMessageReq {
  UserText: string;
  System?: string | undefined;
  Unit?: ModelUnit | undefined;
}

export interface PlanStartReq {
  Task: string;
  ModuleId?: string | undefined;
}

export interface PlanSubmitReq {
  Title: string;
  Body: string;
  Tasks?: PlanTaskRef[] | undefined;
  Policy?: PlanPolicy | undefined;
}

export interface PlanTaskRef {
  Id: string;
  Subject: string;
  Description?: string | undefined;
  DependsOn?: string[] | undefined;
  ActiveForm?: string | undefined;
  Status: string;
}

export interface PlanPolicy {
  Mode: string;
  AllowedPrompts?: AllowedPrompt[] | undefined;
}

export interface AllowedPrompt {
  Tool: string;
  Prompt: string;
}

export interface AgentMessagesListReq {
  SinceIdx?: number | undefined;
  BeforeIdx?: number | undefined;
  Limit?: number | undefined;
}

export interface ForkResult {
  Summary: string;
  SearchCount: number;
  ReadCount: number;
  Iterations: number;
  InputTokens: number;
  OutputTokens: number;
  CacheCreationInputTokens?: number | undefined;
  CacheReadInputTokens?: number | undefined;
  StepsJson?: string | undefined;
  EntriesJson?: string | undefined;
  FileChanges?: TurnFileChange[] | undefined;
}

export interface WorkItem {
  Type: string;
  Target: string;
}

export interface ForkChildProgress {
  Phase: string;
  SearchCount: number;
  ReadCount: number;
  InputTokens: number;
  OutputTokens: number;
  CacheCreationInputTokens?: number | undefined;
  CacheReadInputTokens?: number | undefined;
  ActiveWork: WorkItem[];
  SummaryText?: string | undefined;
  Action?: string | undefined;
  ActionTarget?: string | undefined;
  StepId?: string | undefined;
}

export interface Task {
  Id: string;
  Subject: string;
  Status: string;
  ActiveForm?: string | undefined;
}

export interface AgentTaskCreateReq {
  Subject: string;
  ActiveForm?: string | undefined;
}

export interface AgentTaskCreateResp {
  Task: Task;
}

export interface AgentTaskUpdateReq {
  Id: string;
  Status: string;
}

export interface AgentTaskCancelReq {
  Id: string;
}

export interface AgentTaskCancelResp {
  Task: Task;
}

export interface AgentTaskUpdateResp {
  Task: Task;
}

export interface AgentTaskListReq {

}

export interface AgentTaskListResp {
  Tasks: Task[];
}

export interface AgentTaskDeleteReq {
  Id: string;
}

export interface AgentTaskDeleteResp {

}

export interface AgentStatusNotifyReq {

}

export interface ImageGenerateReq {
  Prompt: string;
  Provider?: string | undefined;
  Model?: string | undefined;
  Quality?: string | undefined;
  Size?: string | undefined;
  AspectRatio?: string | undefined;
  InputImage?: string | undefined;
  ReferenceImages?: string[] | undefined;
}

export interface ImageRecognizeReq {
  Image: string;
  Prompt?: string | undefined;
  Provider?: string | undefined;
  Model?: string | undefined;
  Aggregator?: string | undefined;
}

export interface AgentOpenGlobalBrowserReq {
  Url: string;
}

export interface AgentOpenGlobalBrowserResp {
  Opened: boolean;
  Url: string;
  Error: string;
}

export interface AgentShowPageThumbnailReq {
  Url: string;
  Title?: string | undefined;
}

export interface AgentShowPageThumbnailResp {
  Url: string;
  Title: string;
  FilePath?: string | undefined;
  Error: string;
}

export interface AgentOpenSshSessionReq {
  HostId: string;
}

export interface AgentOpenSshSessionResp {
  SessionId: string;
  HostId: string;
  HostName: string;
  Connected: boolean;
  Error: string;
}

export interface AgentListCallablesReq {
  Query?: string | undefined;
  Limit?: number | undefined;
}

export interface AgentListCallablesResp {
  Items: CallableInterface[];
}

export interface AgentInvokeCallableReq {
  Service: string;
  CallID: string;
  Payload: Record<string, unknown>;
  AppID?: string | undefined;
  AgentID?: string | undefined;
  Role?: string | undefined;
  ProjectID?: string | undefined;
  RequestID?: string | undefined;
}

export interface AgentInvokeCallableResp {
  Result: unknown;
  Error?: string | undefined;
}

export interface AgentFrontendDebugReq {
  Operation?: string | undefined;
  Script?: string | undefined;
}

export interface AgentFrontendDebugResp {
  Result: unknown;
  Error?: string | undefined;
}

export interface AgentInspectActorReq {
  ActorPath: string;
}

export interface AgentInspectActorResp {
  ActorID: string;
  ActorType?: string | undefined;
  CellState?: string | undefined;
  BusinessState?: string | undefined;
  OwnerQueueDepth?: number | undefined;
  OwnerQueueCapacity?: number | undefined;
  SystemQueueDepth?: number | undefined;
  SystemQueueCapacity?: number | undefined;
  ReplyQueueDepth?: number | undefined;
  ReplyQueueCapacity?: number | undefined;
  PendingInvokes?: number | undefined;
  Invoke?: InvokeDiagnostics | undefined;
  Health?: string | undefined;
  Error?: string | undefined;
}

export interface InvokeDiagnostics {
  InFlight: number;
  InFlightByMode: Record<string, number>;
  InFlightByCall: Record<string, number>;
  Registered: number;
  Completed: number;
  Errored: number;
  SendFailed: number;
  ClosedEarly: number;
  EvictedStalled: number;
  EvictedCapacity: number;
  DroppedFrames: number;
  RecentEvictions?: InvokeEvictionRecord[] | undefined;
}

export interface InvokeEvictionRecord {
  CallID?: string | undefined;
  Mode?: string | undefined;
  CorID?: string | undefined;
  Reason?: string | undefined;
  Drops?: number | undefined;
  At?: string | undefined;
}

export interface AgentCaptureProfileReq {
  Profile: string;
  Seconds?: number | undefined;
  Debug?: number | undefined;
  Diff?: boolean | undefined;
  TopN?: number | undefined;
}

export interface AgentCaptureProfileResp {
  Profile: string;
  Text?: string | undefined;
  Truncated?: boolean | undefined;
  Error?: string | undefined;
}

export interface AgentGoalSubmitReq {
  InterpretedGoal: string;
}

export interface AgentGoalSubmitResp {
  RequestId: string;
}

export interface AgentGoalCardSubmitReq {
  CardId: string;
  InterpretedGoal?: string | undefined;
}

export interface AgentGoalCardSubmitResp {
  RequestId: string;
}

export interface GoalSummary {
  Condition?: string | undefined;
  Status?: string | undefined;
  Confirmed?: boolean | undefined;
  BoundTaskCardId?: string | undefined;
  TurnCount?: number | undefined;
  MaxTurns?: number | undefined;
  Outputs?: Record<string, unknown> | undefined;
}

export interface AgentWorkflowStartReq {
  MapCardId: string;
}

export interface AgentWorkflowStartResp {
  MapCardId: string;
}

export interface AgentWorkflowStopReq {

}

export interface AgentWorkflowStopResp {
  MapCardId: string;
  ResiduePath?: string | undefined;
}

export interface AgentWorkflowPauseAllReq {

}

export interface AgentWorkflowPauseAllResp {
  PausedCount: number;
  SkippedCount: number;
}

export interface AgentInternalAssignGoalReq {
  Condition: string;
  InterpretedGoal?: string | undefined;
  MaxTurns?: number | undefined;
  BoundTaskCardId?: string | undefined;
  PromptPrelude?: string | undefined;
}

export interface AgentInternalAssignGoalResp {
  Goal: GoalSummary;
}

export interface AgentInternalResumeFromReviewReq {
  Feedback: string;
}

export interface WorkflowPlanSubmitReq {
  Title: string;
  Body: string;
  NonCoding?: boolean | undefined;
}

export interface WorkflowPlanSubmitResp {
  PlanCardId: string;
  MapCardId: string;
}

export interface VideoGenerateReq {
  Prompt: string;
  Provider?: string | undefined;
  Model?: string | undefined;
  Duration?: string | undefined;
  AspectRatio?: string | undefined;
  ReferenceImages?: string[] | undefined;
  ReferenceVideos?: string[] | undefined;
  ReferenceAudios?: string[] | undefined;
}

export interface AppMediaGenResp {
  Path: string;
  MimeType: string;
  SizeBytes: number;
  Provider: string;
  Model: string;
}
