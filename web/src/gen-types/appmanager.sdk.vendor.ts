// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface AppManagerSdkVendorReq {
  ProjectId?: string | undefined;
  CallerAgentId?: string | undefined;
  AppDir?: string | undefined;
}

export interface AppManagerSdkVendorResp {
  Vendored: boolean;
  Path?: string | undefined;
  Error?: string | undefined;
}
