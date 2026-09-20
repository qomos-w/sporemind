// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function contentDetail(client: GosporeClient, req: systemTypes.ContentDetailReq, opts?: InvokeOptions): Promise<systemTypes.ContentDetailResp> {
  return client.invoke<systemTypes.ContentDetailReq, systemTypes.ContentDetailResp>("cloudaccount.content_detail", req, { reqSchemaId: 4392, resSchemaId: 4393, ...opts });
}

export const contentDetail_meta = {
  callable: "cloudaccount.content_detail",
  name: "content_detail",
  reqSchemaId: 4392,
  resSchemaId: 4393,
} as const;

export async function contentInstall(client: GosporeClient, req: systemTypes.ContentInstallReq, opts?: InvokeOptions): Promise<systemTypes.ContentInstallResp> {
  return client.invoke<systemTypes.ContentInstallReq, systemTypes.ContentInstallResp>("cloudaccount.content_install", req, { reqSchemaId: 4394, resSchemaId: 4395, ...opts });
}

export const contentInstall_meta = {
  callable: "cloudaccount.content_install",
  name: "content_install",
  reqSchemaId: 4394,
  resSchemaId: 4395,
} as const;

export async function contentSearch(client: GosporeClient, req: systemTypes.ContentSearchReq, opts?: InvokeOptions): Promise<systemTypes.ContentSearchResp> {
  return client.invoke<systemTypes.ContentSearchReq, systemTypes.ContentSearchResp>("cloudaccount.content_search", req, { reqSchemaId: 4390, resSchemaId: 4391, ...opts });
}

export const contentSearch_meta = {
  callable: "cloudaccount.content_search",
  name: "content_search",
  reqSchemaId: 4390,
  resSchemaId: 4391,
} as const;

export async function getEntitlements(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.CloudAccountGetEntitlementsResp> {
  return client.invoke<void, systemTypes.CloudAccountGetEntitlementsResp>("cloudaccount.get_entitlements", undefined, { resSchemaId: 4388, ...opts });
}

export async function link(client: GosporeClient, req: systemTypes.CloudAccountLinkReq, opts?: InvokeOptions): Promise<systemTypes.CloudAccountStatus> {
  return client.invoke<systemTypes.CloudAccountLinkReq, systemTypes.CloudAccountStatus>("cloudaccount.link", req, { reqSchemaId: 4386, resSchemaId: 4385, ...opts });
}

export const link_meta = {
  callable: "cloudaccount.link",
  name: "link",
  reqSchemaId: 4386,
  resSchemaId: 4385,
} as const;

export async function redeem(client: GosporeClient, req: systemTypes.CdkeyRedeemReq, opts?: InvokeOptions): Promise<systemTypes.CdkeyRedeemResp> {
  return client.invoke<systemTypes.CdkeyRedeemReq, systemTypes.CdkeyRedeemResp>("cloudaccount.redeem", req, { reqSchemaId: 4398, resSchemaId: 4399, ...opts });
}

export const redeem_meta = {
  callable: "cloudaccount.redeem",
  name: "redeem",
  reqSchemaId: 4398,
  resSchemaId: 4399,
} as const;

export async function sessionToken(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.CloudAccountSessionTokenResp> {
  return client.invoke<void, systemTypes.CloudAccountSessionTokenResp>("cloudaccount.session_token", undefined, { resSchemaId: 4401, ...opts });
}

export async function status(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.CloudAccountStatus> {
  return client.invoke<void, systemTypes.CloudAccountStatus>("cloudaccount.status", undefined, { resSchemaId: 4385, ...opts });
}

export async function sync(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.CloudAccountStatus> {
  return client.invoke<void, systemTypes.CloudAccountStatus>("cloudaccount.sync", undefined, { resSchemaId: 4385, ...opts });
}

export async function unlink(client: GosporeClient, opts?: InvokeOptions): Promise<systemTypes.CloudAccountUnlinkResp> {
  return client.invoke<void, systemTypes.CloudAccountUnlinkResp>("cloudaccount.unlink", undefined, { resSchemaId: 4387, ...opts });
}

