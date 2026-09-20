// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { AppManifest, AppObjectDescriptor } from './app';

export interface SporeAppLoadReq {
  Manifest: AppManifest;
  EntryModule: string;
  Modules: Record<string, string>;
  Assets?: Record<string, Uint8Array> | undefined;
  SchemaDescriptors?: Record<string, AppObjectDescriptor> | undefined;
  PackageHash?: string | undefined;
}

export interface SporeAppUnloadReq {
  Id: string;
}

export interface SporeAppReloadReq {
  Id: string;
  EntryModule: string;
  Modules: Record<string, string>;
  Assets?: Record<string, Uint8Array> | undefined;
  SchemaDescriptors?: Record<string, AppObjectDescriptor> | undefined;
  PackageHash?: string | undefined;
  ExpectedStateVersion?: number | undefined;
  MigratedState?: Record<string, unknown> | undefined;
}

export interface SporeAppReloadResp {
  Id: string;
  Version: string;
  StateVersion?: number | undefined;
}

export interface SporeAppStateReq {
  Id: string;
}

export interface SporeAppStateSetReq {
  Id: string;
  State: Record<string, string>;
}

export interface SporeAppInvokeReq {
  Id: string;
  Callable: string;
  Payload: Uint8Array;
  AgentId?: string | undefined;
  Role?: string | undefined;
  ProjectId?: string | undefined;
  RequestId?: string | undefined;
  RouteDepth?: number | undefined;
  RouteToken?: string | undefined;
}

export interface SporeAppInvokeResp {
  Payload: Uint8Array;
}
