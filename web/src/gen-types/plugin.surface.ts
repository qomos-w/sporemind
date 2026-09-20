// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface PluginSurfaceInvokeReq {
  Type: string;
  RequestId: string;
  CallId: string;
  Payload: unknown;
  PluginId?: string | undefined;
  ViewId?: string | undefined;
  RequestSchema?: string | undefined;
  ResponseSchema?: string | undefined;
}

export interface PluginSurfaceInvokeResp {
  Type: string;
  RequestId: string;
  Ok: boolean;
  Result?: unknown | undefined;
  Error?: string | undefined;
}

export interface PluginSurfaceCastReq {
  Type: string;
  RequestId: string;
  CallId: string;
  Payload: unknown;
  PluginId?: string | undefined;
  ViewId?: string | undefined;
  RequestSchema?: string | undefined;
}

export interface PluginSurfaceCastResp {
  Type: string;
  RequestId: string;
  Ok: boolean;
  Error?: string | undefined;
}

export interface PluginSurfaceEmitReq {
  Type: string;
  RequestId: string;
  EventKind: string;
  Payload: unknown;
  PluginId?: string | undefined;
  ViewId?: string | undefined;
  PayloadSchema?: string | undefined;
}

export interface PluginSurfaceEmitResp {
  Type: string;
  RequestId: string;
  Ok: boolean;
  Error?: string | undefined;
}

export interface PluginSurfaceSubscribeReq {
  Type: string;
  RequestId: string;
  EventKind: string;
  PluginId?: string | undefined;
}

export interface PluginSurfaceUnsubscribeReq {
  Type: string;
  RequestId: string;
  EventKind: string;
  PluginId?: string | undefined;
}

export interface PluginSurfaceSubscribeResp {
  Type: string;
  RequestId: string;
  Ok: boolean;
  Error?: string | undefined;
}

export interface PluginSurfaceEventMsg {
  Type: string;
  EventKind: string;
  Payload: unknown;
}

export interface PluginSurfaceAgentActionReq {
  Type: string;
  RequestId: string;
  Action: string;
  PluginId?: string | undefined;
  Kind?: string | undefined;
  TargetAgentId?: string | undefined;
  Payload?: unknown | undefined;
}

export interface PluginSurfaceAgentActionResp {
  Type: string;
  RequestId: string;
  Ok: boolean;
  Result?: unknown | undefined;
  Error?: string | undefined;
}

export interface PluginSurfaceUIActionReq {
  Type: string;
  RequestId: string;
  Action: string;
  PluginId?: string | undefined;
  ViewId?: string | undefined;
  Title?: string | undefined;
  Zone?: string | undefined;
  Text?: string | undefined;
  Count?: number | undefined;
  Level?: string | undefined;
  Body?: string | undefined;
}

export interface PluginSurfaceUIActionResp {
  Type: string;
  RequestId: string;
  Ok: boolean;
  Error?: string | undefined;
}

export interface PluginSurfaceConsoleLogReq {
  Type: string;
  RequestId: string;
  Level: string;
  Message: string;
  PluginId?: string | undefined;
  ViewId?: string | undefined;
}
