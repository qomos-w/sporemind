// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface PluginPanelOpReq {
  PluginId: string;
  Op: string;
  Selector?: string | undefined;
  Text?: string | undefined;
  Expr?: string | undefined;
  TimeoutMs?: number | undefined;
  MaxChars?: number | undefined;
}

export interface PluginPanelOpResp {
  PluginId: string;
  RequestId: string;
  Ok: boolean;
  Result?: string | undefined;
  Reason?: string | undefined;
}

export interface PluginPanelOpPutReq {
  PluginId: string;
  RequestId: string;
  Ok: boolean;
  Result?: string | undefined;
  Reason?: string | undefined;
  Ts: number;
}

export interface PluginPanelOpPutResp {

}
