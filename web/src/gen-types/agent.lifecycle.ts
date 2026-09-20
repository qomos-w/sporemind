// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface AgentPauseReq {
  ToAgentId: string;
  Reason?: string | undefined;
  CallerAgentId?: string | undefined;
}

export interface AgentPauseResp {
  Sent: boolean;
}

export interface AgentResumeReq {
  ToAgentId: string;
  Reason?: string | undefined;
  CallerAgentId?: string | undefined;
}

export interface AgentResumeResp {
  Sent: boolean;
}

export interface AgentUnloadReq {
  AgentId: string;
}

export interface AgentUnloadResp {
  Unloaded: boolean;
}
