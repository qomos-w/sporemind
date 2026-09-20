// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function accountActivate(client: GosporeClient, req: systemTypes.WebSearchAccountActivateReq, opts?: InvokeOptions): Promise<systemTypes.WebSearchAccountActivateResp> {
  return client.invoke<systemTypes.WebSearchAccountActivateReq, systemTypes.WebSearchAccountActivateResp>("websearch.account_activate", req, { reqSchemaId: 4448, resSchemaId: 4449, ...opts });
}

export const accountActivate_meta = {
  callable: "websearch.account_activate",
  name: "account_activate",
  reqSchemaId: 4448,
  resSchemaId: 4449,
} as const;

export async function accountCreate(client: GosporeClient, req: systemTypes.WebSearchAccountCreateReq, opts?: InvokeOptions): Promise<systemTypes.WebSearchAccountCreateResp> {
  return client.invoke<systemTypes.WebSearchAccountCreateReq, systemTypes.WebSearchAccountCreateResp>("websearch.account_create", req, { reqSchemaId: 4442, resSchemaId: 4443, ...opts });
}

export const accountCreate_meta = {
  callable: "websearch.account_create",
  name: "account_create",
  reqSchemaId: 4442,
  resSchemaId: 4443,
} as const;

export async function accountDelete(client: GosporeClient, req: systemTypes.WebSearchAccountDeleteReq, opts?: InvokeOptions): Promise<systemTypes.WebSearchAccountDeleteResp> {
  return client.invoke<systemTypes.WebSearchAccountDeleteReq, systemTypes.WebSearchAccountDeleteResp>("websearch.account_delete", req, { reqSchemaId: 4446, resSchemaId: 4447, ...opts });
}

export const accountDelete_meta = {
  callable: "websearch.account_delete",
  name: "account_delete",
  reqSchemaId: 4446,
  resSchemaId: 4447,
} as const;

export async function accountList(client: GosporeClient, req: systemTypes.WebSearchAccountListReq, opts?: InvokeOptions): Promise<systemTypes.WebSearchAccountListResp> {
  return client.invoke<systemTypes.WebSearchAccountListReq, systemTypes.WebSearchAccountListResp>("websearch.account_list", req, { reqSchemaId: 4440, resSchemaId: 4441, ...opts });
}

export const accountList_meta = {
  callable: "websearch.account_list",
  name: "account_list",
  reqSchemaId: 4440,
  resSchemaId: 4441,
} as const;

export async function accountUpdate(client: GosporeClient, req: systemTypes.WebSearchAccountUpdateReq, opts?: InvokeOptions): Promise<systemTypes.WebSearchAccountUpdateResp> {
  return client.invoke<systemTypes.WebSearchAccountUpdateReq, systemTypes.WebSearchAccountUpdateResp>("websearch.account_update", req, { reqSchemaId: 4444, resSchemaId: 4445, ...opts });
}

export const accountUpdate_meta = {
  callable: "websearch.account_update",
  name: "account_update",
  reqSchemaId: 4444,
  resSchemaId: 4445,
} as const;

export async function download(client: GosporeClient, req: systemTypes.WebDownloadReq, opts?: InvokeOptions): Promise<systemTypes.WebDownloadResp> {
  return client.invoke<systemTypes.WebDownloadReq, systemTypes.WebDownloadResp>("websearch.download", req, { reqSchemaId: 4453, resSchemaId: 4454, ...opts });
}

export const download_meta = {
  callable: "websearch.download",
  name: "download",
  reqSchemaId: 4453,
  resSchemaId: 4454,
} as const;

export async function fetch(client: GosporeClient, req: systemTypes.WebFetchReq, opts?: InvokeOptions): Promise<systemTypes.WebFetchResp> {
  return client.invoke<systemTypes.WebFetchReq, systemTypes.WebFetchResp>("websearch.fetch", req, { reqSchemaId: 4450, resSchemaId: 4452, ...opts });
}

export const fetch_meta = {
  callable: "websearch.fetch",
  name: "fetch",
  reqSchemaId: 4450,
  resSchemaId: 4452,
} as const;

export async function providerList(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.WebSearchProviderListResp> {
  return client.invoke<void, systemTypes.WebSearchProviderListResp>("websearch.provider_list", undefined, { resSchemaId: 4437, ...opts });
}

export async function search(client: GosporeClient, req: systemTypes.WebSearchReq, opts?: InvokeOptions): Promise<systemTypes.WebSearchResp> {
  return client.invoke<systemTypes.WebSearchReq, systemTypes.WebSearchResp>("websearch.search", req, { reqSchemaId: 4432, resSchemaId: 4433, ...opts });
}

export const search_meta = {
  callable: "websearch.search",
  name: "search",
  reqSchemaId: 4432,
  resSchemaId: 4433,
} as const;

