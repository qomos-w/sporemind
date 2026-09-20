// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function create(client: GosporeClient, req: systemTypes.FrpManagerCreateReq, opts?: InvokeOptions): Promise<systemTypes.FrpInstance> {
  return client.invoke<systemTypes.FrpManagerCreateReq, systemTypes.FrpInstance>("frpmanager.create", req, { reqSchemaId: 790, resSchemaId: 789, ...opts });
}

export const create_meta = {
  callable: "frpmanager.create",
  name: "create",
  reqSchemaId: 790,
  resSchemaId: 789,
} as const;

export async function detect(client: GosporeClient, req: systemTypes.FrpManagerDetectReq, opts?: InvokeOptions): Promise<systemTypes.FrpManagerDetectResp> {
  return client.invoke<systemTypes.FrpManagerDetectReq, systemTypes.FrpManagerDetectResp>("frpmanager.detect", req, { reqSchemaId: 798, resSchemaId: 799, ...opts });
}

export const detect_meta = {
  callable: "frpmanager.detect",
  name: "detect",
  reqSchemaId: 798,
  resSchemaId: 799,
} as const;

export async function get(client: GosporeClient, req: systemTypes.FrpManagerGetReq, opts?: InvokeOptions): Promise<systemTypes.FrpInstance> {
  return client.invoke<systemTypes.FrpManagerGetReq, systemTypes.FrpInstance>("frpmanager.get", req, { reqSchemaId: 792, resSchemaId: 789, ...opts });
}

export const get_meta = {
  callable: "frpmanager.get",
  name: "get",
  reqSchemaId: 792,
  resSchemaId: 789,
} as const;

export async function list(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.FrpManagerListResp> {
  return client.invoke<void, systemTypes.FrpManagerListResp>("frpmanager.list", undefined, { resSchemaId: 793, ...opts });
}

export async function remove(client: GosporeClient, req: systemTypes.FrpManagerRemoveReq, opts?: InvokeOptions): Promise<systemTypes.FrpInstance> {
  return client.invoke<systemTypes.FrpManagerRemoveReq, systemTypes.FrpInstance>("frpmanager.remove", req, { reqSchemaId: 791, resSchemaId: 789, ...opts });
}

export const remove_meta = {
  callable: "frpmanager.remove",
  name: "remove",
  reqSchemaId: 791,
  resSchemaId: 789,
} as const;

export async function start(client: GosporeClient, req: systemTypes.FrpManagerStartReq, opts?: InvokeOptions): Promise<systemTypes.FrpInstance> {
  return client.invoke<systemTypes.FrpManagerStartReq, systemTypes.FrpInstance>("frpmanager.start", req, { reqSchemaId: 796, resSchemaId: 789, ...opts });
}

export const start_meta = {
  callable: "frpmanager.start",
  name: "start",
  reqSchemaId: 796,
  resSchemaId: 789,
} as const;

export async function stop(client: GosporeClient, req: systemTypes.FrpManagerStopReq, opts?: InvokeOptions): Promise<systemTypes.FrpInstance> {
  return client.invoke<systemTypes.FrpManagerStopReq, systemTypes.FrpInstance>("frpmanager.stop", req, { reqSchemaId: 797, resSchemaId: 789, ...opts });
}

export const stop_meta = {
  callable: "frpmanager.stop",
  name: "stop",
  reqSchemaId: 797,
  resSchemaId: 789,
} as const;

export async function update(client: GosporeClient, req: systemTypes.FrpManagerUpdateReq, opts?: InvokeOptions): Promise<systemTypes.FrpInstance> {
  return client.invoke<systemTypes.FrpManagerUpdateReq, systemTypes.FrpInstance>("frpmanager.update", req, { reqSchemaId: 795, resSchemaId: 789, ...opts });
}

export const update_meta = {
  callable: "frpmanager.update",
  name: "update",
  reqSchemaId: 795,
  resSchemaId: 789,
} as const;

