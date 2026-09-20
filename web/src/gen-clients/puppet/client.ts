// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function edit(client: GosporeClient, req: systemTypes.PuppetEditReq, opts?: InvokeOptions): Promise<systemTypes.PuppetEditResp> {
  return client.invoke<systemTypes.PuppetEditReq, systemTypes.PuppetEditResp>("puppet.edit", req, { reqSchemaId: 4562, resSchemaId: 4563, ...opts });
}

export const edit_meta = {
  callable: "puppet.edit",
  name: "edit",
  reqSchemaId: 4562,
  resSchemaId: 4563,
} as const;

