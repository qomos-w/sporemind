// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function invoke(client: GosporeClient, req: systemTypes.SporeAppInvokeReq, opts?: InvokeOptions): Promise<systemTypes.SporeAppInvokeResp> {
  return client.invoke<systemTypes.SporeAppInvokeReq, systemTypes.SporeAppInvokeResp>("sporeapp.invoke", req, { reqSchemaId: 2198, resSchemaId: 2199, ...opts });
}

export const invoke_meta = {
  callable: "sporeapp.invoke",
  name: "invoke",
  reqSchemaId: 2198,
  resSchemaId: 2199,
} as const;

export async function reload(client: GosporeClient, req: systemTypes.SporeAppReloadReq, opts?: InvokeOptions): Promise<systemTypes.SporeAppReloadResp> {
  return client.invoke<systemTypes.SporeAppReloadReq, systemTypes.SporeAppReloadResp>("sporeapp.reload", req, { reqSchemaId: 2194, resSchemaId: 2195, ...opts });
}

export const reload_meta = {
  callable: "sporeapp.reload",
  name: "reload",
  reqSchemaId: 2194,
  resSchemaId: 2195,
} as const;

export async function state(client: GosporeClient, req: systemTypes.SporeAppStateReq, opts?: InvokeOptions): Promise<Record<string, any>> {
  return client.invoke<systemTypes.SporeAppStateReq, Record<string, any>>("sporeapp.state", req, { reqSchemaId: 2196, ...opts });
}

export const state_meta = {
  callable: "sporeapp.state",
  name: "state",
  reqSchemaId: 2196,
} as const;

export async function stateSet(client: GosporeClient, req: systemTypes.SporeAppStateSetReq, opts?: InvokeOptions): Promise<Record<string, any>> {
  return client.invoke<systemTypes.SporeAppStateSetReq, Record<string, any>>("sporeapp.state_set", req, { reqSchemaId: 2197, ...opts });
}

export const stateSet_meta = {
  callable: "sporeapp.state_set",
  name: "state_set",
  reqSchemaId: 2197,
} as const;

