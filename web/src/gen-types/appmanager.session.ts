// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface AppSessionCreateReq {
  AppId: string;
  ViewId: string;
  Origin?: string | undefined;
}

export interface AppSessionCreateResp {
  SessionId: string;
  AppId: string;
  ViewId: string;
  AgentId?: string | undefined;
  ProjectId?: string | undefined;
  Origin: string;
  ExpiresAt: number;
  Nonce: string;
  Generation: number;
  Token: string;
  BackendUrl?: string | undefined;
  CookieToken?: string | undefined;
}

export interface AppSessionResolveReq {
  Token: string;
}

export interface AppSessionResolveResp {
  SessionId: string;
  AppId: string;
  ViewId: string;
  AgentId?: string | undefined;
  ProjectId?: string | undefined;
  Origin: string;
  ExpiresAt: number;
  Nonce: string;
  Generation: number;
}

export interface AppSessionRevokeReq {
  Token: string;
}

export interface AppSessionRevokeResp {

}

export interface AppRouteTokenReq {
  SourceAppId: string;
  TargetAppId: string;
  Callable: string;
  AgentId?: string | undefined;
  Role?: string | undefined;
  ProjectId?: string | undefined;
}

export interface AppRouteTokenResp {
  Token: string;
  ExpiresAt: number;
}
