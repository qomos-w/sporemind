// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { AppStatus } from './app';

export interface AppManagerRegisterProjectReq {
  ProjectId?: string | undefined;
  AppId?: string | undefined;
  EntryModule?: string | undefined;
  CallerAgentId?: string | undefined;
  AppDir?: string | undefined;
}

export interface AppManagerRegisterProjectResp {
  Status: AppStatus;
  Warnings?: string[] | undefined;
}

export interface AppManagerReloadProjectReq {
  ProjectId?: string | undefined;
  AppId: string;
  EntryModule?: string | undefined;
  ExpectedStateVersion?: number | undefined;
  AgentId?: string | undefined;
  RequestId?: string | undefined;
  AppDir?: string | undefined;
  CallerAgentId?: string | undefined;
}

export interface AppManagerReloadProjectResp {
  Status: AppStatus;
  StateVersion: number;
}
