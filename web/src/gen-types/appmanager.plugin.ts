// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { AppStatus } from './app';

export interface AppManagerPluginLoadReq {
  Id: string;
}

export interface AppManagerPluginLoadResp {
  Status: AppStatus;
}

export interface AppManagerPluginUnloadReq {
  Id: string;
}

export interface AppManagerPluginUnloadResp {
  Status: AppStatus;
}
