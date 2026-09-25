// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function list(client: GosporeClient, req: systemTypes.PuppetParamListReq, opts?: InvokeOptions): Promise<systemTypes.PuppetParamListResp> {
  return client.invoke<systemTypes.PuppetParamListReq, systemTypes.PuppetParamListResp>("puppet.param.list", req, { reqSchemaId: 4732, resSchemaId: 4733, ...opts });
}

export const list_meta = {
  callable: "puppet.param.list",
  name: "list",
  reqSchemaId: 4732,
  resSchemaId: 4733,
} as const;

