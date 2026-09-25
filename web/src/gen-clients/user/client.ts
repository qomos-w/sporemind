// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function authLogin(client: GosporeClient, req: systemTypes.AuthLoginReq, opts?: InvokeOptions): Promise<systemTypes.AuthLoginResp> {
  return client.invoke<systemTypes.AuthLoginReq, systemTypes.AuthLoginResp>("user.auth_login", req, { reqSchemaId: 1747, resSchemaId: 1748, ...opts });
}

export const authLogin_meta = {
  callable: "user.auth_login",
  name: "auth_login",
  reqSchemaId: 1747,
  resSchemaId: 1748,
} as const;

export async function authMe(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.AccountView> {
  return client.invoke<void, systemTypes.AccountView>("user.auth_me", undefined, { resSchemaId: 1745, ...opts });
}

export async function authRefresh(client: GosporeClient, req: systemTypes.AuthRefreshReq, opts?: InvokeOptions): Promise<systemTypes.AuthRefreshResp> {
  return client.invoke<systemTypes.AuthRefreshReq, systemTypes.AuthRefreshResp>("user.auth_refresh", req, { reqSchemaId: 1749, resSchemaId: 1750, ...opts });
}

export const authRefresh_meta = {
  callable: "user.auth_refresh",
  name: "auth_refresh",
  reqSchemaId: 1749,
  resSchemaId: 1750,
} as const;

export async function authRegister(client: GosporeClient, req: systemTypes.AuthRegisterReq, opts?: InvokeOptions): Promise<systemTypes.AccountView> {
  return client.invoke<systemTypes.AuthRegisterReq, systemTypes.AccountView>("user.auth_register", req, { reqSchemaId: 1746, resSchemaId: 1745, ...opts });
}

export const authRegister_meta = {
  callable: "user.auth_register",
  name: "auth_register",
  reqSchemaId: 1746,
  resSchemaId: 1745,
} as const;

export async function create(client: GosporeClient, req: systemTypes.AccountCreateReq, opts?: InvokeOptions): Promise<systemTypes.AccountView> {
  return client.invoke<systemTypes.AccountCreateReq, systemTypes.AccountView>("user.create", req, { reqSchemaId: 1753, resSchemaId: 1745, ...opts });
}

export const create_meta = {
  callable: "user.create",
  name: "create",
  reqSchemaId: 1753,
  resSchemaId: 1745,
} as const;

export async function groupCreate(client: GosporeClient, req: systemTypes.GroupCreateReq, opts?: InvokeOptions): Promise<systemTypes.Group> {
  return client.invoke<systemTypes.GroupCreateReq, systemTypes.Group>("user.group_create", req, { reqSchemaId: 1759, resSchemaId: 1757, ...opts });
}

export const groupCreate_meta = {
  callable: "user.group_create",
  name: "group_create",
  reqSchemaId: 1759,
  resSchemaId: 1757,
} as const;

export async function groupList(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.GroupListResp> {
  return client.invoke<void, systemTypes.GroupListResp>("user.group_list", undefined, { resSchemaId: 1758, ...opts });
}

export async function groupRemove(client: GosporeClient, req: systemTypes.GroupDeleteReq, opts?: InvokeOptions): Promise<systemTypes.Group> {
  return client.invoke<systemTypes.GroupDeleteReq, systemTypes.Group>("user.group_remove", req, { reqSchemaId: 1761, resSchemaId: 1757, ...opts });
}

export const groupRemove_meta = {
  callable: "user.group_remove",
  name: "group_remove",
  reqSchemaId: 1761,
  resSchemaId: 1757,
} as const;

export async function groupUpdate(client: GosporeClient, req: systemTypes.GroupUpdateReq, opts?: InvokeOptions): Promise<systemTypes.Group> {
  return client.invoke<systemTypes.GroupUpdateReq, systemTypes.Group>("user.group_update", req, { reqSchemaId: 1760, resSchemaId: 1757, ...opts });
}

export const groupUpdate_meta = {
  callable: "user.group_update",
  name: "group_update",
  reqSchemaId: 1760,
  resSchemaId: 1757,
} as const;

export async function list(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.AccountListResp> {
  return client.invoke<void, systemTypes.AccountListResp>("user.list", undefined, { resSchemaId: 1752, ...opts });
}

export async function permissionGet(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.PermissionMatrix> {
  return client.invoke<void, systemTypes.PermissionMatrix>("user.permission_get", undefined, { resSchemaId: 1763, ...opts });
}

export async function permissionUpdate(client: GosporeClient, req: systemTypes.PermissionUpdateReq, opts?: InvokeOptions): Promise<systemTypes.PermissionMatrix> {
  return client.invoke<systemTypes.PermissionUpdateReq, systemTypes.PermissionMatrix>("user.permission_update", req, { reqSchemaId: 1764, resSchemaId: 1763, ...opts });
}

export const permissionUpdate_meta = {
  callable: "user.permission_update",
  name: "permission_update",
  reqSchemaId: 1764,
  resSchemaId: 1763,
} as const;

export async function remove(client: GosporeClient, req: systemTypes.AccountDeleteReq, opts?: InvokeOptions): Promise<systemTypes.AccountView> {
  return client.invoke<systemTypes.AccountDeleteReq, systemTypes.AccountView>("user.remove", req, { reqSchemaId: 1755, resSchemaId: 1745, ...opts });
}

export const remove_meta = {
  callable: "user.remove",
  name: "remove",
  reqSchemaId: 1755,
  resSchemaId: 1745,
} as const;

export async function resetPassword(client: GosporeClient, req: systemTypes.AccountResetPasswordReq, opts?: InvokeOptions): Promise<systemTypes.AccountView> {
  return client.invoke<systemTypes.AccountResetPasswordReq, systemTypes.AccountView>("user.reset_password", req, { reqSchemaId: 1756, resSchemaId: 1745, ...opts });
}

export const resetPassword_meta = {
  callable: "user.reset_password",
  name: "reset_password",
  reqSchemaId: 1756,
  resSchemaId: 1745,
} as const;

export async function update(client: GosporeClient, req: systemTypes.AccountUpdateReq, opts?: InvokeOptions): Promise<systemTypes.AccountView> {
  return client.invoke<systemTypes.AccountUpdateReq, systemTypes.AccountView>("user.update", req, { reqSchemaId: 1754, resSchemaId: 1745, ...opts });
}

export const update_meta = {
  callable: "user.update",
  name: "update",
  reqSchemaId: 1754,
  resSchemaId: 1745,
} as const;

