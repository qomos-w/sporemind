// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { UsageData } from './aigen.part1';

export interface AgentModeState {
  BoundTaskCardId?: string | undefined;
  ActiveWorkflowMapCardId?: string | undefined;
  ActiveWorkflowWorktreeID?: string | undefined;
  MemoryMounted?: boolean | undefined;
}

export interface ActiveWorkflow {
  MapCardId: string;
  WorktreeID?: string | undefined;
}

export interface PendingWorkflowStart {
  MapCardId: string;
  Destination: string;
  InterpretedGoal: string;
  FrontierSummary: string;
  RequestId: string;
  NonCoding?: boolean | undefined;
}

export interface TurnAssessment {
  Decision: string;
  Reason?: string | undefined;
  Evidence?: string[] | undefined;
  Outputs?: Record<string, unknown> | undefined;
}

export interface TurnLifecycleEvent {
  Kind: string;
  TurnId: string;
  State: string;
  Revision: number;
  TurnOrder: number;
  StartedAt: string;
  CompletedAt?: string | undefined;
  Error?: string | undefined;
  PauseReason?: string | undefined;
  Payload?: Record<string, unknown> | undefined;
}

export interface SessionRequestRecord {
  Id: string;
  Kind: string;
  Seq: number;
  StartedAt?: string | undefined;
  CompletedAt?: string | undefined;
  TurnId?: string | undefined;
  Usage?: UsageData | undefined;
}

export interface ActiveSchedulerEntry {
  SchedulerCardID: string;
  SchedulerName: string;
}

export interface CompactionLockState {
  TurnID: string;
  Trigger: string;
  StartedAt: string;
  Seq: number;
}

export interface AgentBindAppReq {
  AppId: string;
  SystemPrompt?: string | undefined;
  BundleCardIds?: string[] | undefined;
}
