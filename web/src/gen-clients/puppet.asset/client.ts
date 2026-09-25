// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function commit(client: GosporeClient, req: systemTypes.PuppetAssetCommitReq, opts?: InvokeOptions): Promise<systemTypes.PuppetAssetCommitResp> {
  return client.invoke<systemTypes.PuppetAssetCommitReq, systemTypes.PuppetAssetCommitResp>("puppet.asset.commit", req, { reqSchemaId: 4698, resSchemaId: 4699, ...opts });
}

export const commit_meta = {
  callable: "puppet.asset.commit",
  name: "commit",
  reqSchemaId: 4698,
  resSchemaId: 4699,
} as const;

export async function list(client: GosporeClient, req: systemTypes.PuppetAssetListReq, opts?: InvokeOptions): Promise<systemTypes.PuppetAssetListResp> {
  return client.invoke<systemTypes.PuppetAssetListReq, systemTypes.PuppetAssetListResp>("puppet.asset.list", req, { reqSchemaId: 4694, resSchemaId: 4695, ...opts });
}

export const list_meta = {
  callable: "puppet.asset.list",
  name: "list",
  reqSchemaId: 4694,
  resSchemaId: 4695,
} as const;

export async function read(client: GosporeClient, req: systemTypes.PuppetAssetReadReq, opts?: InvokeOptions): Promise<systemTypes.PuppetAssetReadResp> {
  return client.invoke<systemTypes.PuppetAssetReadReq, systemTypes.PuppetAssetReadResp>("puppet.asset.read", req, { reqSchemaId: 4724, resSchemaId: 4725, ...opts });
}

export const read_meta = {
  callable: "puppet.asset.read",
  name: "read",
  reqSchemaId: 4724,
  resSchemaId: 4725,
} as const;

export async function reject(client: GosporeClient, req: systemTypes.PuppetAssetRejectReq, opts?: InvokeOptions): Promise<systemTypes.PuppetAssetRejectResp> {
  return client.invoke<systemTypes.PuppetAssetRejectReq, systemTypes.PuppetAssetRejectResp>("puppet.asset.reject", req, { reqSchemaId: 4700, resSchemaId: 4701, ...opts });
}

export const reject_meta = {
  callable: "puppet.asset.reject",
  name: "reject",
  reqSchemaId: 4700,
  resSchemaId: 4701,
} as const;

export async function stage(client: GosporeClient, req: systemTypes.PuppetAssetStageReq, opts?: InvokeOptions): Promise<systemTypes.PuppetAssetStageResp> {
  return client.invoke<systemTypes.PuppetAssetStageReq, systemTypes.PuppetAssetStageResp>("puppet.asset.stage", req, { reqSchemaId: 4696, resSchemaId: 4697, ...opts });
}

export const stage_meta = {
  callable: "puppet.asset.stage",
  name: "stage",
  reqSchemaId: 4696,
  resSchemaId: 4697,
} as const;

