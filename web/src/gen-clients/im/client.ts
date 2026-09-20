// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function send(client: GosporeClient, req: systemTypes.ImSendReq, opts?: InvokeOptions): Promise<systemTypes.ImSendResp> {
  return client.invoke<systemTypes.ImSendReq, systemTypes.ImSendResp>("im.send", req, { reqSchemaId: 4707, resSchemaId: 4708, ...opts });
}

export const send_meta = {
  callable: "im.send",
  name: "send",
  reqSchemaId: 4707,
  resSchemaId: 4708,
} as const;

export async function status(client: GosporeClient, req: systemTypes.ImStatusReq, opts?: InvokeOptions): Promise<systemTypes.ImStatusResp> {
  return client.invoke<systemTypes.ImStatusReq, systemTypes.ImStatusResp>("im.status", req, { reqSchemaId: 4705, resSchemaId: 4706, ...opts });
}

export const status_meta = {
  callable: "im.status",
  name: "status",
  reqSchemaId: 4705,
  resSchemaId: 4706,
} as const;

