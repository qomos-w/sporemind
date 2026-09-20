// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function configGet(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.StoreClientConfigGetResp> {
  return client.invoke<void, systemTypes.StoreClientConfigGetResp>("storeclient.config_get", undefined, { resSchemaId: 6659, ...opts });
}

export async function configSet(client: GosporeClient, req: systemTypes.StoreClientConfigSetReq, opts?: InvokeOptions): Promise<systemTypes.StoreClientConfigSetResp> {
  return client.invoke<systemTypes.StoreClientConfigSetReq, systemTypes.StoreClientConfigSetResp>("storeclient.config_set", req, { reqSchemaId: 6660, resSchemaId: 6661, ...opts });
}

export const configSet_meta = {
  callable: "storeclient.config_set",
  name: "config_set",
  reqSchemaId: 6660,
  resSchemaId: 6661,
} as const;

export async function index(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.StoreIndexResp> {
  return client.invoke<void, systemTypes.StoreIndexResp>("storeclient.index", undefined, { resSchemaId: 6663, ...opts });
}

export async function install(client: GosporeClient, req: systemTypes.StoreInstallReq, opts?: InvokeOptions): Promise<systemTypes.StoreInstallResp> {
  return client.invoke<systemTypes.StoreInstallReq, systemTypes.StoreInstallResp>("storeclient.install", req, { reqSchemaId: 6667, resSchemaId: 6668, ...opts });
}

export const install_meta = {
  callable: "storeclient.install",
  name: "install",
  reqSchemaId: 6667,
  resSchemaId: 6668,
} as const;

