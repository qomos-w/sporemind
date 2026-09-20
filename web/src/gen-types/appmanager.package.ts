// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { AppManifest, AppObjectDescriptor } from './app';

export interface AppManagerProjectPackageReq {
  ProjectId: string;
  AppId?: string | undefined;
  EntryModule?: string | undefined;
  AppDir?: string | undefined;
  CallerAgentId?: string | undefined;
}

export interface AppManagerProjectPackageResp {
  Manifest: AppManifest;
  EntryModule: string;
  Modules: Record<string, string>;
  Assets?: Record<string, Uint8Array> | undefined;
  SchemaDescriptors?: Record<string, AppObjectDescriptor> | undefined;
  PackageHash: string;
}
