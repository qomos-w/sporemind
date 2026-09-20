// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { ModelUnit } from './aigen.part1';
import { GoalSummary } from './agent';

export interface WorkspaceAgentSpawnAssignReq {
  To?: string | undefined;
  AgentKind: string;
  InterpretedGoal?: string | undefined;
  BoundTaskCardId?: string | undefined;
  MaxTurns?: number | undefined;
  ProjectId?: string | undefined;
  CallerAgentId?: string | undefined;
  PermissionMode?: string | undefined;
  Unit?: ModelUnit | undefined;
  WorktreeBranch?: string | undefined;
}

export interface WorkspaceAgentSpawnAssignResp {
  AgentActorId: string;
  DisplayName: string;
  Goal: GoalSummary;
}

export interface WorkspaceAgentAssignReq {
  AgentActorId: string;
  InterpretedGoal?: string | undefined;
  BoundTaskCardId: string;
  MaxTurns?: number | undefined;
  ProjectId?: string | undefined;
  CallerAgentId?: string | undefined;
}

export interface WorkspaceAgentAssignResp {
  Goal: GoalSummary;
}

export interface WorkspaceAgentReviewReq {
  AgentActorId: string;
  Decision: string;
  Feedback?: string | undefined;
  TaskCardId?: string | undefined;
  CallerAgentId?: string | undefined;
}

export interface WorkspaceAgentReviewResp {
  Approved: boolean;
  CardStatus: string;
  ReviewNote?: string | undefined;
}

export interface WorkspaceAgentTerminateReq {
  AgentActorId: string;
  Reason?: string | undefined;
  CallerAgentId?: string | undefined;
}

export interface WorkspaceAgentTerminateResp {
  Deleted: boolean;
}

export interface WorkspaceAgentSpawnByTypeReq {
  AgentKind: string;
  Description: string;
  Prompt: string;
  Unit?: ModelUnit | undefined;
  MaxIterations?: number | undefined;
  ParentTurnId?: string | undefined;
  ParentStepId?: string | undefined;
  ToolUseId?: string | undefined;
  LifecycleScope?: string | undefined;
  CallerAgentId?: string | undefined;
  PermissionMode?: string | undefined;
  PlanEvidence?: string[] | undefined;
}

export interface WorkspaceAgentSpawnByTypeResp {
  ChildActorId: string;
  DisplayName: string;
}

export interface WorkspaceWorkflowStartReq {
  AgentActorId: string;
  MapCardId: string;
  ProjectId?: string | undefined;
  TemplateMapId?: string | undefined;
}

export interface WorkspaceWorkflowStartResp {
  MapCardId: string;
}

export interface WorkspaceGateApproveReq {
  TaskCardId: string;
  Approver: string;
  CallerAgentId: string;
  FormValues?: Record<string, unknown> | undefined;
  CallableId?: string | undefined;
  Note?: string | undefined;
}

export interface WorkspaceGateApproveResp {
  CardStatus: string;
  ApprovedAt: string;
}

export interface WorkspaceGateRejectReq {
  TaskCardId: string;
  Approver: string;
  CallerAgentId: string;
  Reason?: string | undefined;
  CallableId?: string | undefined;
}

export interface WorkspaceGateRejectResp {
  CardStatus: string;
  RejectedAt: string;
}
