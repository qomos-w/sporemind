// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface AppManagerAppExportReq {
  ProjectId: string;
  CallerAgentId?: string | undefined;
  AppId?: string | undefined;
  AppDir?: string | undefined;
}

export interface AppManagerAppExportResp {
  PackageData: Uint8Array;
  PackageHash: string;
  PublicKey: string;
}
