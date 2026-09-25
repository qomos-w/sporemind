// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function accountActivate(client: GosporeClient, req: systemTypes.WebSearchAccountActivateReq, opts?: InvokeOptions): Promise<systemTypes.WebSearchAccountActivateResp> {
  return client.invoke<systemTypes.WebSearchAccountActivateReq, systemTypes.WebSearchAccountActivateResp>("websearch.account_activate", req, { reqSchemaId: 4592, resSchemaId: 4593, ...opts });
}

export const accountActivate_meta = {
  callable: "websearch.account_activate",
  name: "account_activate",
  reqSchemaId: 4592,
  resSchemaId: 4593,
} as const;

export async function accountCreate(client: GosporeClient, req: systemTypes.WebSearchAccountCreateReq, opts?: InvokeOptions): Promise<systemTypes.WebSearchAccountCreateResp> {
  return client.invoke<systemTypes.WebSearchAccountCreateReq, systemTypes.WebSearchAccountCreateResp>("websearch.account_create", req, { reqSchemaId: 4586, resSchemaId: 4587, ...opts });
}

export const accountCreate_meta = {
  callable: "websearch.account_create",
  name: "account_create",
  reqSchemaId: 4586,
  resSchemaId: 4587,
} as const;

export async function accountDelete(client: GosporeClient, req: systemTypes.WebSearchAccountDeleteReq, opts?: InvokeOptions): Promise<systemTypes.WebSearchAccountDeleteResp> {
  return client.invoke<systemTypes.WebSearchAccountDeleteReq, systemTypes.WebSearchAccountDeleteResp>("websearch.account_delete", req, { reqSchemaId: 4590, resSchemaId: 4591, ...opts });
}

export const accountDelete_meta = {
  callable: "websearch.account_delete",
  name: "account_delete",
  reqSchemaId: 4590,
  resSchemaId: 4591,
} as const;

export async function accountList(client: GosporeClient, req: systemTypes.WebSearchAccountListReq, opts?: InvokeOptions): Promise<systemTypes.WebSearchAccountListResp> {
  return client.invoke<systemTypes.WebSearchAccountListReq, systemTypes.WebSearchAccountListResp>("websearch.account_list", req, { reqSchemaId: 4584, resSchemaId: 4585, ...opts });
}

export const accountList_meta = {
  callable: "websearch.account_list",
  name: "account_list",
  reqSchemaId: 4584,
  resSchemaId: 4585,
} as const;

export async function accountUpdate(client: GosporeClient, req: systemTypes.WebSearchAccountUpdateReq, opts?: InvokeOptions): Promise<systemTypes.WebSearchAccountUpdateResp> {
  return client.invoke<systemTypes.WebSearchAccountUpdateReq, systemTypes.WebSearchAccountUpdateResp>("websearch.account_update", req, { reqSchemaId: 4588, resSchemaId: 4589, ...opts });
}

export const accountUpdate_meta = {
  callable: "websearch.account_update",
  name: "account_update",
  reqSchemaId: 4588,
  resSchemaId: 4589,
} as const;

export async function download(client: GosporeClient, req: systemTypes.WebDownloadReq, opts?: InvokeOptions): Promise<systemTypes.WebDownloadResp> {
  return client.invoke<systemTypes.WebDownloadReq, systemTypes.WebDownloadResp>("websearch.download", req, { reqSchemaId: 4597, resSchemaId: 4598, ...opts });
}

export const download_meta = {
  callable: "websearch.download",
  name: "download",
  reqSchemaId: 4597,
  resSchemaId: 4598,
} as const;

export async function fetch(client: GosporeClient, req: systemTypes.WebFetchReq, opts?: InvokeOptions): Promise<systemTypes.WebFetchResp> {
  return client.invoke<systemTypes.WebFetchReq, systemTypes.WebFetchResp>("websearch.fetch", req, { reqSchemaId: 4594, resSchemaId: 4596, ...opts });
}

export const fetch_meta = {
  callable: "websearch.fetch",
  name: "fetch",
  reqSchemaId: 4594,
  resSchemaId: 4596,
} as const;

export async function providerList(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.WebSearchProviderListResp> {
  return client.invoke<void, systemTypes.WebSearchProviderListResp>("websearch.provider_list", undefined, { resSchemaId: 4581, ...opts });
}

export async function search(client: GosporeClient, req: systemTypes.WebSearchReq, opts?: InvokeOptions): Promise<systemTypes.WebSearchResp> {
  return client.invoke<systemTypes.WebSearchReq, systemTypes.WebSearchResp>("websearch.search", req, { reqSchemaId: 4576, resSchemaId: 4577, ...opts });
}

export const search_meta = {
  callable: "websearch.search",
  name: "search",
  reqSchemaId: 4576,
  resSchemaId: 4577,
} as const;

