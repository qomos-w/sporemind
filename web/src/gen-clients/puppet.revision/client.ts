// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function log(client: GosporeClient, req: systemTypes.PuppetRevisionLogReq, opts?: InvokeOptions): Promise<systemTypes.PuppetRevisionLogResp> {
  return client.invoke<systemTypes.PuppetRevisionLogReq, systemTypes.PuppetRevisionLogResp>("puppet.revision.log", req, { reqSchemaId: 4559, resSchemaId: 4560, ...opts });
}

export const log_meta = {
  callable: "puppet.revision.log",
  name: "log",
  reqSchemaId: 4559,
  resSchemaId: 4560,
} as const;

