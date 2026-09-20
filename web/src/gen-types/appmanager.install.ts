// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { AppStatus } from './app';

export interface AppManagerInstallLocalReq {
  Path?: string | undefined;
  PackageData?: Uint8Array | undefined;
}

export interface AppManagerInstallLocalResp {
  Status: AppStatus;
}
