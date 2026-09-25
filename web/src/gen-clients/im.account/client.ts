// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function create(client: GosporeClient, req: systemTypes.ImAccountCreateReq, opts?: InvokeOptions): Promise<systemTypes.ImAccountCreateResp> {
  return client.invoke<systemTypes.ImAccountCreateReq, systemTypes.ImAccountCreateResp>("im.account.create", req, { reqSchemaId: 4836, resSchemaId: 4837, ...opts });
}

export const create_meta = {
  callable: "im.account.create",
  name: "create",
  reqSchemaId: 4836,
  resSchemaId: 4837,
} as const;

export async function _delete(client: GosporeClient, req: systemTypes.ImAccountDeleteReq, opts?: InvokeOptions): Promise<systemTypes.ImAccountDeleteResp> {
  return client.invoke<systemTypes.ImAccountDeleteReq, systemTypes.ImAccountDeleteResp>("im.account.delete", req, { reqSchemaId: 4840, resSchemaId: 4841, ...opts });
}

export const _delete_meta = {
  callable: "im.account.delete",
  name: "delete",
  reqSchemaId: 4840,
  resSchemaId: 4841,
} as const;

export async function list(client: GosporeClient, req: systemTypes.ImAccountListReq, opts?: InvokeOptions): Promise<systemTypes.ImAccountListResp> {
  return client.invoke<systemTypes.ImAccountListReq, systemTypes.ImAccountListResp>("im.account.list", req, { reqSchemaId: 4834, resSchemaId: 4835, ...opts });
}

export const list_meta = {
  callable: "im.account.list",
  name: "list",
  reqSchemaId: 4834,
  resSchemaId: 4835,
} as const;

export async function update(client: GosporeClient, req: systemTypes.ImAccountUpdateReq, opts?: InvokeOptions): Promise<systemTypes.ImAccountUpdateResp> {
  return client.invoke<systemTypes.ImAccountUpdateReq, systemTypes.ImAccountUpdateResp>("im.account.update", req, { reqSchemaId: 4838, resSchemaId: 4839, ...opts });
}

export const update_meta = {
  callable: "im.account.update",
  name: "update",
  reqSchemaId: 4838,
  resSchemaId: 4839,
} as const;

