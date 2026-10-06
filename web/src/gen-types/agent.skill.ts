// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { CallableInterface } from './observation';

export interface AgentSkillUseReq {
  SkillId: string;
  Args?: string | undefined;
  Context?: string | undefined;
  TurnId?: string | undefined;
}

export interface AgentSkillUseResp {
  MountId: string;
  Body?: string | undefined;
  SkillId?: string | undefined;
  Warning?: string | undefined;
  Result?: string | undefined;
}

export interface AgentEvalReq {
  Script: string;
  Args?: unknown[] | undefined;
}

export interface AgentEvalResp {
  Result?: unknown | undefined;
  Error?: string | undefined;
}

export interface AgentEvalSyntaxReq {
  Lang?: string | undefined;
}

export interface AgentEvalSyntaxResp {
  Lang: string;
  Markdown: string;
}

export interface AgentEvalCallablesReq {
  Query?: string | undefined;
  Limit?: number | undefined;
}

export interface AgentEvalCallablesResp {
  Items: CallableInterface[];
  Total: number;
}

export interface AgentScriptSaveReq {
  Name: string;
  Script: string;
  Description?: string | undefined;
}

export interface AgentScriptSaveResp {
  Name: string;
  Updated: boolean;
}

export interface AgentScriptDeleteReq {
  Name: string;
}

export interface AgentScriptDeleteResp {
  Deleted: boolean;
}

export interface AgentScriptReadReq {
  Name: string;
}

export interface AgentScriptReadResp {
  Name: string;
  Description: string;
  Script: string;
}
