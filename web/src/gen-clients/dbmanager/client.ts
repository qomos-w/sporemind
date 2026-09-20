// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function profileGet(client: GosporeClient, req: systemTypes.DbProfileGetReq, opts?: InvokeOptions): Promise<systemTypes.DbProfileGetResp> {
  return client.invoke<systemTypes.DbProfileGetReq, systemTypes.DbProfileGetResp>("dbmanager.profile_get", req, { reqSchemaId: 5926, resSchemaId: 5927, ...opts });
}

export const profileGet_meta = {
  callable: "dbmanager.profile_get",
  name: "profile_get",
  reqSchemaId: 5926,
  resSchemaId: 5927,
} as const;

export async function profileList(client: GosporeClient, req: systemTypes.DbProfileListReq, opts?: InvokeOptions): Promise<systemTypes.DbProfileListResp> {
  return client.invoke<systemTypes.DbProfileListReq, systemTypes.DbProfileListResp>("dbmanager.profile_list", req, { reqSchemaId: 5924, resSchemaId: 5925, ...opts });
}

export const profileList_meta = {
  callable: "dbmanager.profile_list",
  name: "profile_list",
  reqSchemaId: 5924,
  resSchemaId: 5925,
} as const;

export async function profileRemove(client: GosporeClient, req: systemTypes.DbProfileRemoveReq, opts?: InvokeOptions): Promise<systemTypes.DbProfileRemoveResp> {
  return client.invoke<systemTypes.DbProfileRemoveReq, systemTypes.DbProfileRemoveResp>("dbmanager.profile_remove", req, { reqSchemaId: 5928, resSchemaId: 5929, ...opts });
}

export const profileRemove_meta = {
  callable: "dbmanager.profile_remove",
  name: "profile_remove",
  reqSchemaId: 5928,
  resSchemaId: 5929,
} as const;

export async function profileSave(client: GosporeClient, req: systemTypes.DbProfileSaveReq, opts?: InvokeOptions): Promise<systemTypes.DbProfileSaveResp> {
  return client.invoke<systemTypes.DbProfileSaveReq, systemTypes.DbProfileSaveResp>("dbmanager.profile_save", req, { reqSchemaId: 5922, resSchemaId: 5923, ...opts });
}

export const profileSave_meta = {
  callable: "dbmanager.profile_save",
  name: "profile_save",
  reqSchemaId: 5922,
  resSchemaId: 5923,
} as const;

