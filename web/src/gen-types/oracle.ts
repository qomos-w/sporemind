// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { ModelUnit } from './aigen.part1';
import { ServiceInfo } from './observation';

export interface OracleCapabilityDiscoverReq {
  AgentKind: string;
  Intent?: string | undefined;
  Task?: string | undefined;
  CompressedHistory?: string | undefined;
  InstalledSkillIDs?: string[] | undefined;
  AvailableToolNames?: string[] | undefined;
  Limit?: number | undefined;
}

export interface CapabilityCandidate {
  Kind: string;
  Id: string;
  Name: string;
  RelevanceScore?: number | undefined;
  Rationale?: string | undefined;
}

export interface OracleCapabilityDiscoverResp {
  Items: CapabilityCandidate[];
}

export interface OracleCapabilityExplainReq {
  Id: string;
  AgentKind?: string | undefined;
  Intent?: string | undefined;
  Task?: string | undefined;
}

export interface OracleCapabilityExplainResp {
  Summary: string;
  ExpandMode?: string | undefined;
  WhenToUse?: string | undefined;
  Examples?: string | undefined;
  RelatedIds?: string[] | undefined;
}

export interface Diagnostic {
  Id: string;
  Severity: string;
  Source: string;
  Message: string;
  Timestamp: string;
  AgentId?: string | undefined;
  TurnId?: string | undefined;
  StepId?: string | undefined;
  CallableId?: string | undefined;
  TargetService?: string | undefined;
  ToolUseId?: string | undefined;
  Input?: string | undefined;
  Output?: string | undefined;
  Unit?: ModelUnit | undefined;
  HttpStatus?: number | undefined;
  RawData?: string | undefined;
}

export interface OracleReportDiagnosticReq {
  Severity: string;
  Source: string;
  Message: string;
  AgentId?: string | undefined;
  TurnId?: string | undefined;
  StepId?: string | undefined;
  CallableId?: string | undefined;
  TargetService?: string | undefined;
  ToolUseId?: string | undefined;
  Input?: string | undefined;
  Output?: string | undefined;
  Unit?: ModelUnit | undefined;
  HttpStatus?: number | undefined;
  RawData?: string | undefined;
}

export interface DiagnosticSummary {
  Id: string;
  Severity: string;
  Source: string;
  Message: string;
  Timestamp: string;
  AgentId?: string | undefined;
  TurnId?: string | undefined;
  StepId?: string | undefined;
  CallableId?: string | undefined;
  TargetService?: string | undefined;
  ToolUseId?: string | undefined;
  Input?: string | undefined;
  Output?: string | undefined;
  Unit?: ModelUnit | undefined;
  HttpStatus?: number | undefined;
  RawData?: string | undefined;
}

export interface OracleListDiagnosticsReq {
  Severity?: string | undefined;
  Source?: string | undefined;
  AgentId?: string | undefined;
  TurnId?: string | undefined;
  Since?: string | undefined;
  Limit?: number | undefined;
  Offset?: number | undefined;
}

export interface OracleListDiagnosticsResp {
  Items: DiagnosticSummary[];
}

export interface OracleGetDiagnosticReq {
  Id: string;
}

export interface OracleSearchServicesReq {
  Query?: string | undefined;
  Limit?: number | undefined;
}

export interface OracleSearchServicesResp {
  Items: ServiceInfo[];
}
