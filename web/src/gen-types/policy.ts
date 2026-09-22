// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { ModelUnit } from './aigen.part1';

export interface PolicyQuestion {
  Type: string;
  Instructions: string;
  Choices?: Record<string, string> | undefined;
  Levels?: string[] | undefined;
}

export interface PolicyDecideReq {
  State: string;
  Questions: Record<string, PolicyQuestion>;
  Backend?: string | undefined;
}

export interface PolicyAnswer {
  Type: string;
  Choice?: string | undefined;
  Score?: number | undefined;
  Noul?: number | undefined;
  Probabilities?: Record<string, number> | undefined;
  Confidence?: number | undefined;
  Backend: string;
  Calibrated: boolean;
}

export interface PolicyDecideResp {
  Answers: Record<string, PolicyAnswer>;
  Backend: string;
  Degraded: boolean;
  LatencyMs: number;
  FailoverReason?: string | undefined;
  Error?: string | undefined;
}

export interface PolicyJevConfig {
  ApiKey?: string | undefined;
  Model?: string | undefined;
  Endpoint?: string | undefined;
}

export interface PolicyLLMConfig {
  Unit?: ModelUnit | undefined;
}

export interface PolicyConfigureReq {
  Backend?: string | undefined;
  Jev?: PolicyJevConfig | undefined;
  LLM?: PolicyLLMConfig | undefined;
}

export interface PolicyConfigureResp {
  Status: PolicyStatusResp;
}

export interface PolicyStatusReq {

}

export interface PolicyStatusResp {
  Backend: string;
  JevConfigured: boolean;
  LLMConfigured: boolean;
  LLMUnit?: ModelUnit | undefined;
  JevModel?: string | undefined;
  JevEndpoint?: string | undefined;
}
