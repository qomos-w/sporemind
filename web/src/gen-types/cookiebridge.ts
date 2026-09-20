// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface CookieBridgePairingInfoReq {

}

export interface CookieBridgePairingInfoResp {
  McpUrl: string;
  PushUrl: string;
  PendingUrl: string;
  PairingToken: string;
  Port: number;
}

export interface CookieBridgeRegenTokenReq {

}

export interface CookieBridgeRegenTokenResp {
  PairingToken: string;
}
