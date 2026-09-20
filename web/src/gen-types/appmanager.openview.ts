// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface AppManagerOpenViewReq {
  Id: string;
  ViewId?: string | undefined;
}

export interface AppManagerOpenViewResp {
  Opened: boolean;
  ViewId?: string | undefined;
  Error?: string | undefined;
}
