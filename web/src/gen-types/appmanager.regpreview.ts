// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { AppManifest } from './app';

export interface AppManagerRegistrationPreviewReq {
  Manifest?: AppManifest | undefined;
  ProjectId?: string | undefined;
  AppId?: string | undefined;
  AppDir?: string | undefined;
  CallerAgentId?: string | undefined;
  Path?: string | undefined;
  Slug?: string | undefined;
}

export interface AppManagerRegistrationPreviewResp {
  Manifest: AppManifest;
}
