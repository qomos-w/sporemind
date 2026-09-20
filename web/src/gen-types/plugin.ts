// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { AppManifest, AppStatus } from './app';

export interface PluginAbi {
  Name: string;
  Version: number;
  Encoding: string;
  InvokeSymbol: string;
  ContractVersion?: string | undefined;
  Isolation?: string | undefined;
  TrustClass?: string | undefined;
  Signer?: string | undefined;
  Capabilities?: string[] | undefined;
}

export interface PluginLoadReq {
  Manifest: AppManifest;
  Abi: PluginAbi;
}

export interface PluginLoadResp {
  Status: AppStatus;
}

export interface PluginUnloadReq {
  Id: string;
}

export interface PluginAbiInvokeEnvelope {
  Callable: string;
  Payload: Uint8Array;
  RequestId?: string | undefined;
  SessionId?: string | undefined;
  CallSeq?: number | undefined;
}

export interface PluginSdkRequest {
  PluginId: string;
  CallId: string;
  Payload: Uint8Array;
  RequestId?: string | undefined;
  SessionId?: string | undefined;
  CallSeq?: number | undefined;
}

export interface PluginSdkResponse {
  Payload: unknown;
}

export interface PluginInvokeReq {
  Id: string;
  Callable: string;
  Payload: Uint8Array;
  AgentId?: string | undefined;
  Role?: string | undefined;
  ProjectId?: string | undefined;
  WorkspaceId?: string | undefined;
  RequestId?: string | undefined;
  SessionId?: string | undefined;
  CallSeq?: number | undefined;
  TimeoutMs?: number | undefined;
}

export interface PluginInvokeResp {
  Payload: Uint8Array;
}

export interface PluginInvokeChunk {
  Payload: Uint8Array;
  Terminal?: boolean | undefined;
}

export interface PluginEventDeliverReq {
  Kind: string;
  Payload: string;
}

export interface PluginEventDeliverResp {
  Delivered: number;
  Failures?: string[] | undefined;
}

export interface PluginRuntimeStateEvent {
  PluginId: string;
  Transport: string;
  Phase: string;
  HotReloading?: boolean | undefined;
  Crashed?: boolean | undefined;
  Error?: string | undefined;
  ExitCode?: number | undefined;
  Restartable?: boolean | undefined;
}

export interface PluginLogPutReq {
  PluginId: string;
  Level: string;
  Message: string;
  ViewId?: string | undefined;
}

export interface PluginLogEntry {
  Time: string;
  Level: string;
  Source: string;
  Message: string;
}

export interface PluginLogsReq {
  PluginId: string;
  Limit?: number | undefined;
}

export interface PluginLogsResp {
  PluginId: string;
  Generation: number;
  Dropped?: number | undefined;
  Entries?: PluginLogEntry[] | undefined;
  ProcessState?: string | undefined;
  Crash?: string | undefined;
}
