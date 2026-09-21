// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function stats(client: GosporeClient, req: systemTypes.eventStatsReq, opts?: InvokeOptions): Promise<systemTypes.eventStatsResp> {
  return client.invoke<systemTypes.eventStatsReq, systemTypes.eventStatsResp>("gospore.events.stats", req, { reqSchemaId: 1771, resSchemaId: 1772, ...opts });
}

export const stats_meta = {
  callable: "gospore.events.stats",
  name: "stats",
  reqSchemaId: 1771,
  resSchemaId: 1772,
} as const;

export async function *subscribeInstance(client: GosporeClient, req: systemTypes.eventSubscribeInstanceReq, opts?: InvokeOptions): AsyncIterable<any> {
  yield* client.subscribe<any>("gospore.events.subscribe_instance", req, { reqSchemaId: 67, ...opts });
}

export async function *subscribeService(client: GosporeClient, req: systemTypes.eventSubscribeServiceReq, opts?: InvokeOptions): AsyncIterable<any> {
  yield* client.subscribe<any>("gospore.events.subscribe_service", req, { reqSchemaId: 66, ...opts });
}

