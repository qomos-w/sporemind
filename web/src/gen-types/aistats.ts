// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { UsageData } from './aigen.part1';

export interface AIStatsRecord {
  Id: string;
  WorkspaceId: string;
  ProjectId: string;
  AgentId: string;
  SessionId: string;
  TurnId: string;
  RequestId: string;
  Provider: string;
  Model: string;
  ResponseModel?: string | undefined;
  ResponseId?: string | undefined;
  ClientRequestId?: string | undefined;
  Usage?: UsageData | undefined;
  StopReason?: string | undefined;
  ErrorCode?: string | undefined;
  ErrorMessage?: string | undefined;
  LatencyMs?: number | undefined;
  FirstTokenMs?: number | undefined;
  StartedAt: string;
  CompletedAt: string;
  Tags?: Record<string, string> | undefined;
}

export interface TurnRequestStat {
  Id: string;
  Provider: string;
  Model: string;
  ResponseModel?: string | undefined;
  ResponseId?: string | undefined;
  RequestId?: string | undefined;
  Usage?: UsageData | undefined;
  StopReason?: string | undefined;
  ErrorCode?: string | undefined;
  ErrorMessage?: string | undefined;
  LatencyMs?: number | undefined;
  FirstTokenMs?: number | undefined;
  StartedAt?: string | undefined;
  CompletedAt?: string | undefined;
}

export interface AIStatsCostRate {
  Provider: string;
  Model: string;
  Version: number;
  UpdatedAt: string;
  CostInput: number;
  CostOutput: number;
  CostCacheRead: number;
  CostCacheWrite?: number | undefined;
  Tiers?: AIStatsCostTier[] | undefined;
}

export interface AIStatsCostTier {
  InputTokensAbove: number;
  CostInput: number;
  CostOutput: number;
  CostCacheRead: number;
  CostCacheWrite?: number | undefined;
}

export interface AIStatsCounters {
  RequestCount: number;
  ErrorCount: number;
  LatencySumMs: number;
  InputTokens: number;
  OutputTokens: number;
  TotalTokens: number;
  CacheCreationInputTokens: number;
  CacheReadInputTokens: number;
  ReasoningTokens: number;
  CostInput: number;
  CostOutput: number;
  CostCacheRead: number;
  CostCacheWrite: number;
  CostTotal: number;
}

export interface AIStatsSessionAggregate {
  SessionId: string;
  Counters: AIStatsCounters;
  LastRecordAt: string;
}

export interface AIStatsProjectAggregate {
  ProjectId: string;
  Counters: AIStatsCounters;
  LastRecordAt: string;
}

export interface AIStatsWorkspaceAggregate {
  WorkspaceId: string;
  Counters: AIStatsCounters;
  LastRecordAt: string;
}

export interface AIStatsProviderAggregate {
  Provider: string;
  Counters: AIStatsCounters;
  LastRecordAt: string;
}

export interface AIStatsModelAggregate {
  Provider: string;
  Model: string;
  Counters: AIStatsCounters;
  LastRecordAt: string;
}

export interface AIStatsRecordReq {
  Record: AIStatsRecord;
}

export interface AIStatsRecordResp {
  Id: string;
}

export interface AIStatsQueryReq {
  Scope: string;
  ScopeId: string;
  WorkspaceID?: string | undefined;
  Since?: string | undefined;
  Until?: string | undefined;
  Limit?: number | undefined;
  Offset?: number | undefined;
  Order?: string | undefined;
}

export interface AIStatsQueryResp {
  Records: AIStatsRecord[];
  Counters: AIStatsCounters;
  Total: number;
}

export interface AIStatsCostConfigureReq {
  Rate: AIStatsCostRate;
}

export interface AIStatsCostConfigureResp {
  Version: number;
}

export interface AIStatsCostListReq {
  Provider?: string | undefined;
  Model?: string | undefined;
}

export interface AIStatsCostListResp {
  Rates: AIStatsCostRate[];
}

export interface AIStatsExportReq {
  Scope: string;
  ScopeId: string;
  WorkspaceID?: string | undefined;
  Since?: string | undefined;
  Until?: string | undefined;
  Format?: string | undefined;
}

export interface AIStatsExportResp {
  Data: string;
}

export interface AgentSessionStatsReq {
  SessionId?: string | undefined;
  Since?: string | undefined;
  Limit?: number | undefined;
}

export interface AgentSessionStatsResp {
  Counters: AIStatsCounters;
  CacheHitRate: number;
  LatestProvider: string;
  LatestModel: string;
}

export interface AIStatsBackfillReq {
  WorkspaceId: string;
  ProjectId?: string | undefined;
  AgentId?: string | undefined;
  SessionId?: string | undefined;
}

export interface AIStatsBackfillResp {
  RecordCount: number;
}

export interface AIStatsAggregatesReq {
  WorkspaceID?: string | undefined;
}

export interface AIStatsAggregatesResp {
  Workspace: AIStatsCounters;
  Models: AIStatsModelAggregate[];
  Providers: AIStatsProviderAggregate[];
}

export interface AIStatsBucket {
  Start: number;
  End: number;
  Requests: number;
  Errors: number;
  InputTokens: number;
  OutputTokens: number;
  CacheRead: number;
  CacheWrite: number;
  ReasoningTokens: number;
  Cost: number;
  LatencyP50: number;
  LatencyP95: number;
  LatencySumMs: number;
  TtftP50: number;
  ErrorCodes: Record<string, number>;
  Rollup?: boolean | undefined;
  Grain?: string | undefined;
}

export interface AIStatsModelStat {
  Provider: string;
  Model: string;
  Requests: number;
  Errors: number;
  InputTokens: number;
  OutputTokens: number;
  CacheRead: number;
  CacheWrite: number;
  CostTotal: number;
  CostInput: number;
  CostOutput: number;
  CostCacheRead: number;
  CostCacheWrite: number;
  LatencyP95: number;
  TtftP50: number;
  LatencySumMs: number;
}

export interface AIStatsSeriesReq {
  Scope: string;
  ScopeId: string;
  WorkspaceID?: string | undefined;
  Since?: string | undefined;
  Until?: string | undefined;
  ModelFilter?: string | undefined;
  BucketMs: number;
}

export interface AIStatsSeriesResp {
  Buckets: AIStatsBucket[];
  ModelStats: AIStatsModelStat[];
  OverallLatencyP50: number;
  OverallLatencyP95: number;
  OverallTtftP50: number;
  OverallLatencySumMs: number;
  OverallOutputTokens: number;
}
