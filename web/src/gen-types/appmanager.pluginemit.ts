// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface AppManagerPluginEmitReq {
  PluginId: string;
  Event: string;
  Payload: Uint8Array;
}

export interface AppManagerPluginEmitResp {
  Accepted: boolean;
}
