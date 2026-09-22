// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function configure(client: GosporeClient, req: systemTypes.PolicyConfigureReq, opts?: InvokeOptions): Promise<systemTypes.PolicyConfigureResp> {
  return client.invoke<systemTypes.PolicyConfigureReq, systemTypes.PolicyConfigureResp>("policy.configure", req, { reqSchemaId: 6694, resSchemaId: 6695, ...opts });
}

export const configure_meta = {
  callable: "policy.configure",
  name: "configure",
  reqSchemaId: 6694,
  resSchemaId: 6695,
} as const;

export async function decide(client: GosporeClient, req: systemTypes.PolicyDecideReq, opts?: InvokeOptions): Promise<systemTypes.PolicyDecideResp> {
  return client.invoke<systemTypes.PolicyDecideReq, systemTypes.PolicyDecideResp>("policy.decide", req, { reqSchemaId: 6689, resSchemaId: 6691, ...opts });
}

export const decide_meta = {
  callable: "policy.decide",
  name: "decide",
  reqSchemaId: 6689,
  resSchemaId: 6691,
} as const;

export async function status(client: GosporeClient, req: systemTypes.PolicyStatusReq, opts?: InvokeOptions): Promise<systemTypes.PolicyStatusResp> {
  return client.invoke<systemTypes.PolicyStatusReq, systemTypes.PolicyStatusResp>("policy.status", req, { reqSchemaId: 6696, resSchemaId: 6697, ...opts });
}

export const status_meta = {
  callable: "policy.status",
  name: "status",
  reqSchemaId: 6696,
  resSchemaId: 6697,
} as const;

