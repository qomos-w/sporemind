// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { CompactionPolicy, ExploreResult, Session, SessionGoal, StructuredTraceEntry, SummarySegment, Turn, TurnEvent } from './aigen.part2';
import { ChatMessage, MiddlewareEntry, ModelUnit, Step, StepEvent, TurnAction, TurnStatus, UsageData } from './aigen.part1';

export interface AgentSessionImportReq {
  Session: Session;
  SummarySegments?: SummarySegment[] | undefined;
  ExploreResults?: ExploreResult[] | undefined;
  Steps?: Step[] | undefined;
  Goal?: SessionGoal | undefined;
  NextIdx?: number | undefined;
  NextSeq?: number | undefined;
  NextTurnOrder?: number | undefined;
}

export interface AgentSessionImportResp {
  AcceptedTurns: number;
  ActiveHead: number;
}

export interface AgentSessionSummaryReq {
  MaxTurns?: number | undefined;
  KnownStepIDs?: string[] | undefined;
  knownStepEventSeqs?: Record<string, number> | undefined;
  offset?: number | undefined;
  limit?: number | undefined;
}

export interface AgentSessionSummaryResp {
  Turns: Turn[];
  Steps: Step[];
  ActiveTurn: TurnStatus;
  ActiveTurnEvents: TurnEvent[];
  TotalTurns: number;
  HasMoreHistory: boolean;
  AgentState?: string | undefined;
  ExploreResults?: ExploreResult[] | undefined;
  MissingSteps?: Step[] | undefined;
  NextSeq?: number | undefined;
  OpenStepEvents?: StepEvent[] | undefined;
  HasDiscardedSteps?: boolean | undefined;
  Goal?: SessionGoal | undefined;
}

export interface AgentTurnsListReq {
  BeforeTurnId?: string | undefined;
  Limit?: number | undefined;
}

export interface AgentMessagesListResp {
  Messages: ChatMessage[];
  HasMore: boolean;
}

export interface AgentTurnsListResp {
  Turns: Turn[];
  HasMore: boolean;
  Steps?: Step[] | undefined;
}

export interface AgentCompactionConfigureReq {
  CompactionPolicy?: CompactionPolicy | undefined;
  ResetSummary?: boolean | undefined;
}

export interface AgentCompactionConfigureResp {
  CompactionPolicy: CompactionPolicy;
  SummarySegments: SummarySegment[];
  EstimatedTokens?: number | undefined;
  Source?: string | undefined;
}

export interface PermissionPolicy {
  DefaultBehavior?: string | undefined;
  ApproverAgentId?: string | undefined;
  AutoAllowTools?: string[] | undefined;
}

export interface ToolCallSummary {
  Id: string;
  CallableId: string;
  Input?: string | undefined;
  AppId?: string | undefined;
  AppName?: string | undefined;
  Permissions?: string[] | undefined;
  PermissionNote?: string | undefined;
}

export interface PermissionReviewReq {
  TurnId: string;
  ToolCalls: ToolCallSummary[];
}

export interface PermissionReviewResp {
  Allowed: boolean;
  Reason?: string | undefined;
}

export interface TurnHistoryResp {
  Steps: TurnAction[];
  TraceEntries: StructuredTraceEntry[];
  Usage?: UsageData | undefined;
}

export interface TurnMiddlewareResp {
  Entries: MiddlewareEntry[];
}

export interface TurnStatusResp {
  State: string;
  PauseKind?: string | undefined;
  TaskId?: string | undefined;
}

export interface StoragePolicy {
  Enabled: boolean;
  MaxSessionChars?: number | undefined;
  DiscardBatchSize?: number | undefined;
}

export interface AgentStorageConfigureReq {
  StoragePolicy?: StoragePolicy | undefined;
}

export interface AgentStorageConfigureResp {
  StoragePolicy: StoragePolicy;
  Source?: string | undefined;
}

export interface ModelRef {
  kind: string;
  Unit?: ModelUnit | undefined;
  AggregatorID?: string | undefined;
}

export interface ModelSlot {
  Candidates: ModelRef[];
}

export interface DispatchActivity {
  State: string;
  SessionId?: string | undefined;
  AgentId?: string | undefined;
  SlotKind?: string | undefined;
  StartedAt?: number | undefined;
  Depth?: number | undefined;
  AggregatorId?: string | undefined;
}
