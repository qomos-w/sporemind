// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function create(client: GosporeClient, req: systemTypes.ImAccountCreateReq, opts?: InvokeOptions): Promise<systemTypes.ImAccountCreateResp> {
  return client.invoke<systemTypes.ImAccountCreateReq, systemTypes.ImAccountCreateResp>("im.account.create", req, { reqSchemaId: 4692, resSchemaId: 4693, ...opts });
}

export const create_meta = {
  callable: "im.account.create",
  name: "create",
  reqSchemaId: 4692,
  resSchemaId: 4693,
} as const;

export async function _delete(client: GosporeClient, req: systemTypes.ImAccountDeleteReq, opts?: InvokeOptions): Promise<systemTypes.ImAccountDeleteResp> {
  return client.invoke<systemTypes.ImAccountDeleteReq, systemTypes.ImAccountDeleteResp>("im.account.delete", req, { reqSchemaId: 4696, resSchemaId: 4697, ...opts });
}

export const _delete_meta = {
  callable: "im.account.delete",
  name: "delete",
  reqSchemaId: 4696,
  resSchemaId: 4697,
} as const;

export async function list(client: GosporeClient, req: systemTypes.ImAccountListReq, opts?: InvokeOptions): Promise<systemTypes.ImAccountListResp> {
  return client.invoke<systemTypes.ImAccountListReq, systemTypes.ImAccountListResp>("im.account.list", req, { reqSchemaId: 4690, resSchemaId: 4691, ...opts });
}

export const list_meta = {
  callable: "im.account.list",
  name: "list",
  reqSchemaId: 4690,
  resSchemaId: 4691,
} as const;

export async function update(client: GosporeClient, req: systemTypes.ImAccountUpdateReq, opts?: InvokeOptions): Promise<systemTypes.ImAccountUpdateResp> {
  return client.invoke<systemTypes.ImAccountUpdateReq, systemTypes.ImAccountUpdateResp>("im.account.update", req, { reqSchemaId: 4694, resSchemaId: 4695, ...opts });
}

export const update_meta = {
  callable: "im.account.update",
  name: "update",
  reqSchemaId: 4694,
  resSchemaId: 4695,
} as const;

