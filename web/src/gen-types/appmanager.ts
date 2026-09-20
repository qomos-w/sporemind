// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { AppManifest, AppObjectDescriptor, AppStatus } from './app';
import { PluginAbi } from './plugin';

export interface AppManagerRegisterReq {
  Manifest: AppManifest;
  EntryModule: string;
  Modules: Record<string, string>;
  Assets?: Record<string, Uint8Array> | undefined;
  SchemaDescriptors?: Record<string, AppObjectDescriptor> | undefined;
  PackageHash?: string | undefined;
  PackagePath?: string | undefined;
  ArtifactPath?: string | undefined;
  ArtifactHash?: string | undefined;
  Abi?: PluginAbi | undefined;
  Origin?: string | undefined;
}

export interface AppManagerUnregisterReq {
  Id: string;
}

export interface AppManagerGetReq {
  Id: string;
}

export interface AppManagerGetResp {
  Status: AppStatus;
}

export interface AppManagerListReq {

}

export interface AppManagerListResp {
  Items: AppStatus[];
}

export interface AppManagerInvokeReq {
  Id: string;
  Callable: string;
  Payload: Uint8Array;
  AgentId?: string | undefined;
  Role?: string | undefined;
  ProjectId?: string | undefined;
  WorkspaceId?: string | undefined;
  RequestId?: string | undefined;
  SessionId?: string | undefined;
  SessionToken?: string | undefined;
  CallSeq?: number | undefined;
  ExpectedPackageHash?: string | undefined;
  RouteDepth?: number | undefined;
  RouteToken?: string | undefined;
}

export interface AppManagerInvokeResp {
  Payload: Uint8Array;
}

export interface AppManagerCastReq {
  Id: string;
  Event: string;
  Payload: Uint8Array;
  AgentId?: string | undefined;
  Role?: string | undefined;
  ProjectId?: string | undefined;
  SessionToken?: string | undefined;
}

export interface AppManagerCastResp {
  Delivered: number;
}

export interface AppManagerEmitReq {
  Id: string;
  Event: string;
  Payload: Uint8Array;
  AgentId?: string | undefined;
  Role?: string | undefined;
  ProjectId?: string | undefined;
  SessionToken?: string | undefined;
}

export interface AppManagerEmitResp {
  Accepted: boolean;
}

export interface AppEventMessage {
  Id: string;
  Event: string;
  Payload: Uint8Array;
  Sender?: string | undefined;
}

export interface AppManagerAuditReq {
  AppId?: string | undefined;
  Limit?: number | undefined;
}

export interface AppManagerAuditResp {
  Records: AppAuditRecord[];
}

export interface AppAuditRecord {
  Time: string;
  RequestId: string;
  AppId: string;
  Runtime: string;
  AgentId: string;
  Role: string;
  ProjectId: string;
  Callable: string;
  Allowed: boolean;
  Reason: string;
  SessionId?: string | undefined;
  CallSeq?: number | undefined;
}
