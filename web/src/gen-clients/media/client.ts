// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function activateAccount(client: GosporeClient, req: systemTypes.MediaAccountActivateReq, opts?: InvokeOptions): Promise<systemTypes.MediaAccountActivateResp> {
  return client.invoke<systemTypes.MediaAccountActivateReq, systemTypes.MediaAccountActivateResp>("media.activate_account", req, { reqSchemaId: 4506, resSchemaId: 4507, ...opts });
}

export const activateAccount_meta = {
  callable: "media.activate_account",
  name: "activate_account",
  reqSchemaId: 4506,
  resSchemaId: 4507,
} as const;

export async function createAccount(client: GosporeClient, req: systemTypes.MediaAccountCreateReq, opts?: InvokeOptions): Promise<systemTypes.MediaAccountCreateResp> {
  return client.invoke<systemTypes.MediaAccountCreateReq, systemTypes.MediaAccountCreateResp>("media.create_account", req, { reqSchemaId: 4500, resSchemaId: 4501, ...opts });
}

export const createAccount_meta = {
  callable: "media.create_account",
  name: "create_account",
  reqSchemaId: 4500,
  resSchemaId: 4501,
} as const;

export async function deleteAccount(client: GosporeClient, req: systemTypes.MediaAccountDeleteReq, opts?: InvokeOptions): Promise<systemTypes.MediaAccountDeleteResp> {
  return client.invoke<systemTypes.MediaAccountDeleteReq, systemTypes.MediaAccountDeleteResp>("media.delete_account", req, { reqSchemaId: 4504, resSchemaId: 4505, ...opts });
}

export const deleteAccount_meta = {
  callable: "media.delete_account",
  name: "delete_account",
  reqSchemaId: 4504,
  resSchemaId: 4505,
} as const;

export async function listAccounts(client: GosporeClient, req: systemTypes.MediaAccountListReq, opts?: InvokeOptions): Promise<systemTypes.MediaAccountListResp> {
  return client.invoke<systemTypes.MediaAccountListReq, systemTypes.MediaAccountListResp>("media.list_accounts", req, { reqSchemaId: 4498, resSchemaId: 4499, ...opts });
}

export const listAccounts_meta = {
  callable: "media.list_accounts",
  name: "list_accounts",
  reqSchemaId: 4498,
  resSchemaId: 4499,
} as const;

export async function providerModelSet(client: GosporeClient, req: systemTypes.MediaProviderModelSetReq, opts?: InvokeOptions): Promise<systemTypes.MediaProviderModelSetResp> {
  return client.invoke<systemTypes.MediaProviderModelSetReq, systemTypes.MediaProviderModelSetResp>("media.provider_model_set", req, { reqSchemaId: 5680, resSchemaId: 5681, ...opts });
}

export const providerModelSet_meta = {
  callable: "media.provider_model_set",
  name: "provider_model_set",
  reqSchemaId: 5680,
  resSchemaId: 5681,
} as const;

export async function updateAccount(client: GosporeClient, req: systemTypes.MediaAccountUpdateReq, opts?: InvokeOptions): Promise<systemTypes.MediaAccountUpdateResp> {
  return client.invoke<systemTypes.MediaAccountUpdateReq, systemTypes.MediaAccountUpdateResp>("media.update_account", req, { reqSchemaId: 4502, resSchemaId: 4503, ...opts });
}

export const updateAccount_meta = {
  callable: "media.update_account",
  name: "update_account",
  reqSchemaId: 4502,
  resSchemaId: 4503,
} as const;

