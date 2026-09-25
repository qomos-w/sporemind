// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function _delete(client: GosporeClient, req: systemTypes.ImRouteDeleteReq, opts?: InvokeOptions): Promise<systemTypes.ImRouteDeleteResp> {
  return client.invoke<systemTypes.ImRouteDeleteReq, systemTypes.ImRouteDeleteResp>("im.route.delete", req, { reqSchemaId: 4847, resSchemaId: 4848, ...opts });
}

export const _delete_meta = {
  callable: "im.route.delete",
  name: "delete",
  reqSchemaId: 4847,
  resSchemaId: 4848,
} as const;

export async function list(client: GosporeClient, req: systemTypes.ImRouteListReq, opts?: InvokeOptions): Promise<systemTypes.ImRouteListResp> {
  return client.invoke<systemTypes.ImRouteListReq, systemTypes.ImRouteListResp>("im.route.list", req, { reqSchemaId: 4843, resSchemaId: 4844, ...opts });
}

export const list_meta = {
  callable: "im.route.list",
  name: "list",
  reqSchemaId: 4843,
  resSchemaId: 4844,
} as const;

export async function set(client: GosporeClient, req: systemTypes.ImRouteSetReq, opts?: InvokeOptions): Promise<systemTypes.ImRouteSetResp> {
  return client.invoke<systemTypes.ImRouteSetReq, systemTypes.ImRouteSetResp>("im.route.set", req, { reqSchemaId: 4845, resSchemaId: 4846, ...opts });
}

export const set_meta = {
  callable: "im.route.set",
  name: "set",
  reqSchemaId: 4845,
  resSchemaId: 4846,
} as const;

