// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function configGet(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.StoreClientConfigGetResp> {
  return client.invoke<void, systemTypes.StoreClientConfigGetResp>("storeclient.config_get", undefined, { resSchemaId: 6563, ...opts });
}

export async function configSet(client: GosporeClient, req: systemTypes.StoreClientConfigSetReq, opts?: InvokeOptions): Promise<systemTypes.StoreClientConfigSetResp> {
  return client.invoke<systemTypes.StoreClientConfigSetReq, systemTypes.StoreClientConfigSetResp>("storeclient.config_set", req, { reqSchemaId: 6564, resSchemaId: 6565, ...opts });
}

export const configSet_meta = {
  callable: "storeclient.config_set",
  name: "config_set",
  reqSchemaId: 6564,
  resSchemaId: 6565,
} as const;

export async function index(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.StoreIndexResp> {
  return client.invoke<void, systemTypes.StoreIndexResp>("storeclient.index", undefined, { resSchemaId: 6567, ...opts });
}

export async function install(client: GosporeClient, req: systemTypes.StoreInstallReq, opts?: InvokeOptions): Promise<systemTypes.StoreInstallResp> {
  return client.invoke<systemTypes.StoreInstallReq, systemTypes.StoreInstallResp>("storeclient.install", req, { reqSchemaId: 6571, resSchemaId: 6572, ...opts });
}

export const install_meta = {
  callable: "storeclient.install",
  name: "install",
  reqSchemaId: 6571,
  resSchemaId: 6572,
} as const;

