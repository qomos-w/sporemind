// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface PluginDomReq {
  PluginId: string;
}

export interface PluginDomResp {
  PluginId: string;
  Found: boolean;
  Snapshot?: string | undefined;
  CapturedAt?: number | undefined;
  Reason?: string | undefined;
}

export interface PluginDomPutReq {
  PluginId: string;
  Snapshot: string;
  Ts: number;
}

export interface PluginDomPutResp {

}
