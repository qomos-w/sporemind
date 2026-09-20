// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface AgentCapabilityBindingReq {
  AppId: string;
  AgentId: string;
  Callable: string;
  Role: string;
  ProjectId: string;
}

export interface AgentCapabilityBindingResp {
  Allowed: boolean;
  Reason?: string | undefined;
}

export interface AgentSurfaceBindingReq {
  AppId: string;
  AgentId: string;
  Entrypoint: string;
  Projections: string[];
  Events: string[];
}

export interface AgentSurfaceBindingResp {
  Bound: boolean;
}

export interface FreeAgentPolicy {
  AllowCreate: boolean;
  AllowSwitch: boolean;
  AllowMessage: boolean;
  AgentKinds?: string[] | undefined;
}

export interface AgentBindingStatus {
  AppId: string;
  AgentId: string;
  State: string;
  Error?: string | undefined;
}
