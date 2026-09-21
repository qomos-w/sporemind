// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function stats(client: GosporeClient, req: systemTypes.cellStatsReq, opts?: InvokeOptions): Promise<any> {
  return client.invoke<systemTypes.cellStatsReq, any>("gospore.cell.stats", req, { reqSchemaId: 1773, ...opts });
}

export const stats_meta = {
  callable: "gospore.cell.stats",
  name: "stats",
  reqSchemaId: 1773,
} as const;

