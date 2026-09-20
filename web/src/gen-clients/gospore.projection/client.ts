// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function get(client: GosporeClient, req: systemTypes.projectionGetReq, opts?: InvokeOptions): Promise<any> {
  return client.invoke<systemTypes.projectionGetReq, any>("gospore.projection.get", req, { reqSchemaId: 68, ...opts });
}

export const get_meta = {
  callable: "gospore.projection.get",
  name: "get",
  reqSchemaId: 68,
} as const;

export async function *watch(client: GosporeClient, req: systemTypes.projectionWatchReq, opts?: InvokeOptions): AsyncIterable<any> {
  yield* client.subscribe<any>("gospore.projection.watch", req, { reqSchemaId: 69, ...opts });
}

