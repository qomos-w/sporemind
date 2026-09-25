// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function profileGet(client: GosporeClient, req: systemTypes.DbProfileGetReq, opts?: InvokeOptions): Promise<systemTypes.DbProfileGetResp> {
  return client.invoke<systemTypes.DbProfileGetReq, systemTypes.DbProfileGetResp>("dbmanager.profile_get", req, { reqSchemaId: 6070, resSchemaId: 6071, ...opts });
}

export const profileGet_meta = {
  callable: "dbmanager.profile_get",
  name: "profile_get",
  reqSchemaId: 6070,
  resSchemaId: 6071,
} as const;

export async function profileList(client: GosporeClient, req: systemTypes.DbProfileListReq, opts?: InvokeOptions): Promise<systemTypes.DbProfileListResp> {
  return client.invoke<systemTypes.DbProfileListReq, systemTypes.DbProfileListResp>("dbmanager.profile_list", req, { reqSchemaId: 6068, resSchemaId: 6069, ...opts });
}

export const profileList_meta = {
  callable: "dbmanager.profile_list",
  name: "profile_list",
  reqSchemaId: 6068,
  resSchemaId: 6069,
} as const;

export async function profileRemove(client: GosporeClient, req: systemTypes.DbProfileRemoveReq, opts?: InvokeOptions): Promise<systemTypes.DbProfileRemoveResp> {
  return client.invoke<systemTypes.DbProfileRemoveReq, systemTypes.DbProfileRemoveResp>("dbmanager.profile_remove", req, { reqSchemaId: 6072, resSchemaId: 6073, ...opts });
}

export const profileRemove_meta = {
  callable: "dbmanager.profile_remove",
  name: "profile_remove",
  reqSchemaId: 6072,
  resSchemaId: 6073,
} as const;

export async function profileSave(client: GosporeClient, req: systemTypes.DbProfileSaveReq, opts?: InvokeOptions): Promise<systemTypes.DbProfileSaveResp> {
  return client.invoke<systemTypes.DbProfileSaveReq, systemTypes.DbProfileSaveResp>("dbmanager.profile_save", req, { reqSchemaId: 6066, resSchemaId: 6067, ...opts });
}

export const profileSave_meta = {
  callable: "dbmanager.profile_save",
  name: "profile_save",
  reqSchemaId: 6066,
  resSchemaId: 6067,
} as const;

