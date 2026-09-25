// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function pairingInfo(client: GosporeClient, req: systemTypes.CookieBridgePairingInfoReq, opts?: InvokeOptions): Promise<systemTypes.CookieBridgePairingInfoResp> {
  return client.invoke<systemTypes.CookieBridgePairingInfoReq, systemTypes.CookieBridgePairingInfoResp>("cookiebridge.pairing_info", req, { reqSchemaId: 6368, resSchemaId: 6369, ...opts });
}

export const pairingInfo_meta = {
  callable: "cookiebridge.pairing_info",
  name: "pairing_info",
  reqSchemaId: 6368,
  resSchemaId: 6369,
} as const;

export async function regenToken(client: GosporeClient, req: systemTypes.CookieBridgeRegenTokenReq, opts?: InvokeOptions): Promise<systemTypes.CookieBridgeRegenTokenResp> {
  return client.invoke<systemTypes.CookieBridgeRegenTokenReq, systemTypes.CookieBridgeRegenTokenResp>("cookiebridge.regen_token", req, { reqSchemaId: 6370, resSchemaId: 6371, ...opts });
}

export const regenToken_meta = {
  callable: "cookiebridge.regen_token",
  name: "regen_token",
  reqSchemaId: 6370,
  resSchemaId: 6371,
} as const;

