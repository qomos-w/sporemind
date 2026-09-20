// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface AppManagerPanelTopologyReq {
  Id: string;
}

export interface AppManagerPanelTopologyResp {
  AppId: string;
  State: string;
  Generation: number;
  GatewayBase: string;
  PanelUrl: string;
  PluginListener: string;
  AuthNotes: string[];
  ExpectedCodes: string[];
}
