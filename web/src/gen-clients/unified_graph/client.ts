// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function history(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.TopologyHistoryResp> {
  return client.invoke<void, systemTypes.TopologyHistoryResp>("unified_graph.history", undefined, { resSchemaId: 893, ...opts });
}

export async function sync(client: GosporeClient, req: systemTypes.TopologySyncReq, opts?: InvokeOptions): Promise<systemTypes.TopologySyncResp> {
  return client.invoke<systemTypes.TopologySyncReq, systemTypes.TopologySyncResp>("unified_graph.sync", req, { reqSchemaId: 890, resSchemaId: 891, ...opts });
}

export const sync_meta = {
  callable: "unified_graph.sync",
  name: "sync",
  reqSchemaId: 890,
  resSchemaId: 891,
} as const;

