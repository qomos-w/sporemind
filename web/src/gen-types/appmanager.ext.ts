// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { AppManifest, AppObjectDescriptor, AppStatus } from './app';
import { PluginAbi } from './plugin';

export interface AppManagerAgentActionReq {
  Id: string;
  Action: string;
  Kind?: string | undefined;
  TargetAgentId?: string | undefined;
  Payload?: Record<string, unknown> | undefined;
  AgentId?: string | undefined;
  Role?: string | undefined;
  ProjectId?: string | undefined;
  RequestId?: string | undefined;
}

export interface AppManagerAgentActionResp {
  Accepted: boolean;
}

export interface AppManagerReloadReq {
  Id: string;
  EntryModule: string;
  Modules: Record<string, string>;
  Assets?: Record<string, Uint8Array> | undefined;
  SchemaDescriptors?: Record<string, AppObjectDescriptor> | undefined;
  PackageHash?: string | undefined;
  ExpectedStateVersion?: number | undefined;
  MigratedState?: Record<string, unknown> | undefined;
  AgentId?: string | undefined;
  RequestId?: string | undefined;
  CandidateManifest?: AppManifest | undefined;
  CandidateAbi?: PluginAbi | undefined;
  CandidateArtifactPath?: string | undefined;
  CandidateArtifactHash?: string | undefined;
}

export interface AppManagerReloadResp {
  Status: AppStatus;
  StateVersion: number;
}
