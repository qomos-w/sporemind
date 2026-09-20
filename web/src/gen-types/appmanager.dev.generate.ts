// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface AppManagerDevGenerateReq {
  ProjectId?: string | undefined;
  CallerAgentId?: string | undefined;
  AppDir?: string | undefined;
  Template?: boolean | undefined;
}

export interface AppManagerDevGenerateFileEntry {
  Path: string;
  ContentHash: string;
}

export interface AppManagerDevGenerateResp {
  Files: AppManagerDevGenerateFileEntry[];
  AppDefHash: string;
  SDKPath: string;
  FreshSDK: boolean;
  Template: boolean;
  Warnings?: string[] | undefined;
  Error?: string | undefined;
}
